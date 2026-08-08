import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { SessionPausePanel } from "./SessionPausePanel";
import { APP_LOCALES, appCatalogs } from "@/i18n";
import type { SessionFailoverReason, SessionFailoverView, SessionPause, WorkspaceSession } from "@/types/workspace";

const post = vi.fn();
vi.mock("@/lib/api-client", async () => {
	const actual = await vi.importActual<typeof import("@/lib/api-client")>("@/lib/api-client");
	return { ...actual, apiClient: { POST: (...args: unknown[]) => post(...args) } };
});

function session(overrides: Partial<WorkspaceSession> = {}): WorkspaceSession {
	return {
		id: "proj-1",
		workspaceId: "proj",
		workspaceName: "proj",
		title: "worker",
		provider: "claude-code",
		status: "idle",
		updatedAt: new Date().toISOString(),
		activity: { state: "idle", lastActivityAt: "" },
		...overrides,
	} as WorkspaceSession;
}

const pin: SessionPause = {
	incidentId: "limit-abc123",
	reason: "usage_limit",
	detectedBy: "structured_envelope",
	harness: "codex",
	pausedAt: "2026-08-06T12:00:00Z",
	retryAfter: "2026-08-06T18:00:00Z",
};

/**
 * The read model's failover block, contract §9. Every value here is the
 * daemon's: the panel is not allowed to derive any of it.
 */
function failover(overrides: Partial<SessionFailoverView> = {}): SessionFailoverView {
	return {
		available: true,
		roleId: "backend",
		nextTarget: { harness: "kimi", model: "kimi-k2" },
		nextRungIndex: 0,
		attemptsUsed: 0,
		maxAttempts: 8,
		incidentId: "limit-abc123",
		reason: "",
		...overrides,
	};
}

const dead = { activity: { state: "exited", lastActivityAt: "" } } as Partial<WorkspaceSession>;

function renderPanel(s: WorkspaceSession) {
	const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	return render(
		<QueryClientProvider client={qc}>
			<SessionPausePanel session={s} />
		</QueryClientProvider>,
	);
}

const resumeButton = () => screen.getByRole("button", { name: /Resume this session/ });
const continueButton = () => screen.getByRole("button", { name: /Continue this session on/ });
const displayedIncident = () => screen.getByTestId("session-pause-incident").textContent;

beforeEach(() => post.mockReset());

describe("SessionPausePanel", () => {
	it("renders nothing when the session is not paused", () => {
		const { container } = renderPanel(session());
		expect(container).toBeEmptyDOMElement();
	});

	it("shows reason, harness, incident, time and retry information", () => {
		renderPanel(session({ pause: pin }));
		expect(screen.getByText("Usage limit")).toBeInTheDocument();
		expect(screen.getByText("Harness (structured report)")).toBeInTheDocument();
		expect(screen.getByText("codex")).toBeInTheDocument();
		expect(screen.getByText("limit-abc123")).toBeInTheDocument();
		// retryAfter is INFORMATION. Rendering it as a countdown would imply an
		// automatic resume that does not exist.
		expect(screen.getByText(/informational — AO will not resume on its own/)).toBeInTheDocument();
	});

	it("distinguishes paused-with-a-live-agent from paused-with-a-dead-one", () => {
		const live = renderPanel(session({ pause: pin, activity: { state: "idle", lastActivityAt: "" } }));
		expect(screen.getByText("Agent running")).toBeInTheDocument();
		live.unmount();

		renderPanel(session({ pause: pin, activity: { state: "exited", lastActivityAt: "" } }));
		expect(screen.getByText("Agent stopped")).toBeInTheDocument();
		// Resume must not imply restart on the dead cell — that is the whole
		// reason the contract insists on separate controls.
		expect(screen.getByText(/restarting it is a separate step/)).toBeInTheDocument();
	});

	it("submits the incident id it is DISPLAYING", async () => {
		post.mockResolvedValue({ error: undefined, response: { status: 200 } });
		renderPanel(session({ pause: pin }));

		await userEvent.click(resumeButton());
		await waitFor(() => expect(post).toHaveBeenCalledTimes(1));

		const [path, opts] = post.mock.calls[0] as [string, { body: { incidentId: string } }];
		expect(path).toBe("/api/v1/sessions/{sessionId}/resume");
		// Re-reading the current pin at submit time is the stale-resume bug: an
		// action raised for one incident landing after another replaced it would
		// resume the wrong one.
		expect(opts.body.incidentId).toBe("limit-abc123");
	});

	it("surfaces the daemon's own code and message on conflict", async () => {
		post.mockResolvedValue({
			error: { code: "PAUSE_INCIDENT_MISMATCH", message: "A different incident now holds this session" },
			response: { status: 409 },
		});
		renderPanel(session({ pause: pin }));

		await userEvent.click(resumeButton());
		const alert = await screen.findByRole("alert");
		expect(alert).toHaveTextContent("A different incident now holds this session");
		// The CODE has to reach the human too, or "re-read" is indistinguishable
		// from "the daemon broke".
		expect(alert).toHaveTextContent("PAUSE_INCIDENT_MISMATCH");
	});

	it("shows a pending state while the request is in flight", async () => {
		let release: (v: unknown) => void = () => {};
		post.mockReturnValue(new Promise((r) => (release = r)));
		renderPanel(session({ pause: pin }));

		const button = resumeButton();
		await userEvent.click(button);
		await waitFor(() => expect(resumeButton()).toBeDisabled());
		expect(screen.getByText("Resuming…")).toBeInTheDocument();
		release({ error: undefined, response: { status: 200 } });
	});

	it("labels an operator pause differently from a detected one", () => {
		renderPanel(session({ pause: { ...pin, reason: "operator", detectedBy: "operator", harness: undefined } }));
		expect(screen.getByText("Requested by a person")).toBeInTheDocument();
		expect(screen.queryByText("Harness (structured report)")).not.toBeInTheDocument();
	});
});

// PHASE3B_MVP_CONTRACT §2. Continue is a third operation on the same pin, and
// the failure mode it has to be protected from is looking like one of the other
// two: Resume starts nothing, Restart relaunches the SAME harness, Continue is
// the only one that moves the session somewhere else.
describe("SessionPausePanel Continue control", () => {
	it("offers Resume and Continue with a live agent, and adds Restart only where a process died", () => {
		// Paused-live: restarting would offer to relaunch something that never
		// stopped, so it is absent rather than reworded.
		const live = renderPanel(session({ pause: pin, failover: failover() }));
		expect(resumeButton()).toBeInTheDocument();
		expect(continueButton()).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Restart agent" })).not.toBeInTheDocument();
		live.unmount();

		// Paused-dead: all three, and no two of them share a label.
		renderPanel(session({ pause: pin, ...dead, failover: failover() }));
		const names = screen.getAllByRole("button").map((b) => b.textContent?.trim());
		expect(names).toHaveLength(3);
		expect(new Set(names).size).toBe(3);
		expect(resumeButton()).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Restart agent" })).toBeInTheDocument();
		expect(continueButton()).toBeInTheDocument();
	});

	it("labels Continue from the backend's nextTarget, never from a local harness mapping", () => {
		const withModel = renderPanel(
			session({ pause: pin, failover: failover({ nextTarget: { harness: "kimi", model: "kimi-k2" } }) }),
		);
		// Verbatim, both halves. A renderer-side id→name table (the inspector has
		// one, which turns "kimi" into "Kimi") must never touch this label: it
		// would let the button claim a target the host never authorized.
		expect(screen.getByRole("button", { name: /Continue this session on kimi · kimi-k2/ })).toBeInTheDocument();
		expect(screen.getByText("Continue with kimi · kimi-k2")).toBeInTheDocument();
		expect(screen.queryByText(/Kimi/)).not.toBeInTheDocument();
		withModel.unmount();

		// An empty model is "provider default", never a wildcard — the harness
		// stands alone rather than being padded with an invented model name.
		const noModel = renderPanel(
			session({ pause: pin, failover: failover({ nextTarget: { harness: "codex", model: "" } }) }),
		);
		expect(screen.getByText("Continue with codex")).toBeInTheDocument();
		noModel.unmount();

		// A harness id the desktop has never heard of still labels the button.
		renderPanel(
			session({ pause: pin, failover: failover({ nextTarget: { harness: "acme-harness-9", model: "" } }) }),
		);
		expect(screen.getByText("Continue with acme-harness-9")).toBeInTheDocument();
	});

	it("is absent entirely when the daemon sends no failover block", () => {
		renderPanel(session({ pause: pin }));
		expect(resumeButton()).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: /Continue/ })).not.toBeInTheDocument();
	});

	it("posts the incident to /continue with no target in the body", async () => {
		post.mockResolvedValue({ error: undefined, response: { status: 200 } });
		renderPanel(session({ pause: pin, failover: failover() }));

		await userEvent.click(continueButton());
		await waitFor(() => expect(post).toHaveBeenCalledTimes(1));

		const [path, opts] = post.mock.calls[0] as [string, { params: unknown; body: Record<string, unknown> }];
		expect(path).toBe("/api/v1/sessions/{sessionId}/continue");
		// The host picks the rung. A free-form target has to be structurally
		// impossible from the client, not merely rejected by the daemon.
		expect(opts.body).toEqual({ incidentId: "limit-abc123" });
	});

	it("continues the incident it DISPLAYS, even after the session prop moves to a newer one", async () => {
		post.mockResolvedValue({ error: undefined, response: { status: 200 } });
		const view = renderPanel(session({ pause: pin, failover: failover() }));
		expect(displayedIncident()).toBe("limit-abc123");

		// The world moves under the panel: a second incident replaces the first.
		const newer: SessionPause = { ...pin, incidentId: "limit-def456" };
		view.rerender(
			<QueryClientProvider client={new QueryClient()}>
				<SessionPausePanel
					session={session({ pause: newer, failover: failover({ incidentId: "limit-def456" }) })}
				/>
			</QueryClientProvider>,
		);

		const shown = displayedIncident();
		await userEvent.click(continueButton());
		await waitFor(() => expect(post).toHaveBeenCalledTimes(1));

		const [, opts] = post.mock.calls[0] as [string, { body: { incidentId: string } }];
		// What was on screen is what got answered. A ref frozen at mount would
		// leave a stale incident on display; a re-read of the current pin at click
		// time would answer an incident nobody looked at. Both diverge from what
		// the human read, and that divergence is the whole bug.
		expect(opts.body.incidentId).toBe(shown);
		expect(opts.body.incidentId).toBe("limit-def456");
	});

	it("never sends failover.incidentId when it disagrees with the displayed pin", async () => {
		post.mockResolvedValue({ error: undefined, response: { status: 200 } });
		// A preview computed for an older incident than the one holding the pin.
		renderPanel(session({ pause: pin, failover: failover({ incidentId: "limit-STALE" }) }));

		await userEvent.click(continueButton());
		await waitFor(() => expect(post).toHaveBeenCalledTimes(1));

		const [, opts] = post.mock.calls[0] as [string, { body: { incidentId: string } }];
		expect(opts.body.incidentId).toBe(displayedIncident());
		expect(opts.body.incidentId).toBe("limit-abc123");
	});

	it("shows the host-resolved role and attempt budget without deriving either", () => {
		renderPanel(session({ pause: pin, failover: failover({ attemptsUsed: 3, maxAttempts: 8 }) }));
		expect(screen.getByText("backend")).toBeInTheDocument();
		expect(screen.getByText("3 of 8")).toBeInTheDocument();
	});
});

// `available: false` carries a machine-readable reason precisely so the desktop
// can say WHY without inventing prose. A disabled control with no explanation
// is a dead end for the person looking at it.
describe("SessionPausePanel disabled Continue", () => {
	const reasons: ReadonlyArray<[SessionFailoverReason, string]> = [
		["", "Continue is not available for this session."],
		["no_role_pin", "This session carries no role, so there is no failover ladder to follow."],
		["no_ladder", "This role has no failover targets configured."],
		["ladder_exhausted", "Every failover target for this role has already been used for this incident."],
		["limit_reached", "This incident has used all of its failover attempts. The session stays paused."],
		["not_paused", "Continue is only offered while the session is paused."],
		["switch_unsupported", "This harness cannot be switched, so the session cannot move to another target."],
	];

	it.each(reasons)("explains reason %s beside the disabled control", async (reason, copy) => {
		renderPanel(
			session({ pause: pin, failover: failover({ available: false, nextTarget: null, reason }) }),
		);

		const button = screen.getByRole("button", { name: "Continue" });
		expect(button).toBeDisabled();
		expect(screen.getByText(copy)).toBeInTheDocument();
		// The explanation is attached to the control, not merely nearby: a
		// disabled button fires no pointer events, so a tooltip would be
		// unreachable for both mouse and screen reader.
		const describedBy = button.getAttribute("aria-describedby");
		expect(describedBy).toBeTruthy();
		expect(document.getElementById(describedBy as string)).toHaveTextContent(copy);

		await userEvent.click(button);
		expect(post).not.toHaveBeenCalled();
	});

	it("stays disabled when the daemon claims availability but names no rung", () => {
		renderPanel(session({ pause: pin, failover: failover({ available: true, nextTarget: null }) }));
		// The renderer may not invent a target to fill the gap.
		expect(screen.getByRole("button", { name: "Continue" })).toBeDisabled();
	});

	it("explains what Continue does when it IS available", () => {
		renderPanel(session({ pause: pin, failover: failover() }));
		expect(
			screen.getByText(
				"Moves this session to the next authorized target and lifts the pause once that target answers. The role does not change.",
			),
		).toBeInTheDocument();
	});
});

describe("SessionPausePanel while a continuation is in flight", () => {
	it("disables every control and cannot be made to submit twice", async () => {
		let release: (v: unknown) => void = () => {};
		post.mockReturnValue(new Promise((r) => (release = r)));
		renderPanel(session({ pause: pin, ...dead, failover: failover() }));

		const button = continueButton();
		await userEvent.dblClick(button);

		// One request, not two: a second continuation for the same incident would
		// race the first for the same rung.
		expect(post).toHaveBeenCalledTimes(1);
		await waitFor(() => expect(continueButton()).toBeDisabled());
		// All three, not just the one that was clicked. Resume during a
		// continuation would lift the pin the saga is still holding, and Restart
		// would relaunch the source the saga is tearing down.
		expect(resumeButton()).toBeDisabled();
		expect(screen.getByRole("button", { name: "Restart agent" })).toBeDisabled();
		expect(screen.getByText("Continuing…")).toBeInTheDocument();

		release({ error: undefined, response: { status: 200 } });
		await waitFor(() => expect(continueButton()).toBeEnabled());
	});

	it("re-enables the controls after a failure so the human can act again", async () => {
		post.mockResolvedValue({ error: { code: "SWITCH_IN_PROGRESS", message: "busy" }, response: { status: 409 } });
		renderPanel(session({ pause: pin, failover: failover() }));

		await userEvent.click(continueButton());
		await screen.findByRole("alert");
		await waitFor(() => expect(continueButton()).toBeEnabled());
		expect(resumeButton()).toBeEnabled();
	});
});

// Contract §4. The daemon's message says what happened; this copy says what it
// means for the pause and what to do next. No entry offers a retry, because
// nothing in AO retries on its own.
describe("SessionPausePanel error copy", () => {
	const cases: ReadonlyArray<[string, string]> = [
		["PAUSE_INCIDENT_MISMATCH", "A different incident holds this session now. Re-read the pause above"],
		["FAILOVER_NO_TARGET", "No target is left for this incident. The session stays paused."],
		["FAILOVER_LIMIT_REACHED", "This incident has reached its failover limit. The session stays paused."],
		["FAILOVER_ROLE_REQUIRED", "This session carries no role, so there is no ladder to follow."],
		["FAILOVER_RECOVERY_REQUIRED", "An unfinished move from an earlier generation has to be recovered first."],
		["SESSION_NOT_PAUSED", "This session is no longer paused, so there is nothing to answer."],
		["SWITCH_IN_PROGRESS", "Another move is already under way for this session."],
		["SWITCH_NOT_SUPPORTED", "The source or target harness does not support switching."],
		["PAUSE_AGENT_FORBIDDEN", "Only a person may answer a pause."],
		["NOT_A_WORKER", "Only worker sessions move to another target."],
	];

	it.each(cases)("gives %s its own explanation", async (code, copy) => {
		post.mockResolvedValue({ error: { code, message: `daemon said ${code}` }, response: { status: 409 } });
		renderPanel(session({ pause: pin, failover: failover() }));

		await userEvent.click(continueButton());
		const alert = await screen.findByRole("alert");
		expect(alert).toHaveTextContent(`daemon said ${code}`);
		expect(alert).toHaveTextContent(copy);
	});

	it("never suggests a retry or renders a countdown", async () => {
		post.mockResolvedValue({
			error: { code: "FAILOVER_LIMIT_REACHED", message: "limit reached" },
			response: { status: 409 },
		});
		renderPanel(session({ pause: pin, failover: failover({ attemptsUsed: 8 }) }));

		await userEvent.click(continueButton());
		const alert = await screen.findByRole("alert");
		// "Try again" would reintroduce, one layer up, exactly the automatic
		// retry the pause exists to prevent.
		expect(alert.textContent ?? "").not.toMatch(/retry|try again/i);
		expect(within(alert).queryByRole("timer")).not.toBeInTheDocument();
	});

	it("falls back to the daemon's message for a code it has no copy for", async () => {
		post.mockResolvedValue({
			error: { code: "SOMETHING_ENTIRELY_NEW", message: "the daemon explains itself" },
			response: { status: 500 },
		});
		renderPanel(session({ pause: pin, failover: failover() }));

		await userEvent.click(continueButton());
		const alert = await screen.findByRole("alert");
		expect(alert).toHaveTextContent("the daemon explains itself");
		expect(alert).toHaveTextContent("SOMETHING_ENTIRELY_NEW");
	});
});

// A disabled control that falls back to English tells a non-English operator
// nothing, and the reasons are the only place the UI explains itself.
describe("Continue copy across every locale", () => {
	const keys = Object.keys(appCatalogs.en).filter(
		(key) =>
			key.startsWith("inspector.pause.continu") ||
			key.startsWith("inspector.pause.failover") ||
			key.startsWith("inspector.pause.error") ||
			key === "inspector.pause.role",
	);

	const placeholders = (message: string) =>
		[...message.matchAll(/{{\s*([\w.-]+)\s*}}/g)].map((m) => m[1]).sort();

	it("covers the whole Continue surface in all eight catalogs", () => {
		expect(keys.length).toBeGreaterThanOrEqual(33);
		for (const locale of APP_LOCALES) {
			const catalog = appCatalogs[locale];
			for (const key of keys) {
				expect(catalog[key], `${locale} is missing ${key}`).toBeTruthy();
				expect(placeholders(catalog[key]), `${locale} placeholder drift on ${key}`).toEqual(
					placeholders(appCatalogs.en[key]),
				);
			}
		}
	});

	it("leaves no English fallback in a non-English catalog", () => {
		for (const locale of APP_LOCALES) {
			if (locale === "en") continue;
			for (const key of keys) {
				expect(appCatalogs[locale][key], `${locale}.${key} is still English`).not.toBe(appCatalogs.en[key]);
			}
		}
	});
});
