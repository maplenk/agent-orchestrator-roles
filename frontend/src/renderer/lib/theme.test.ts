import { readFileSync } from "node:fs";
import path from "node:path";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
	applyDocumentTheme,
	applyDocumentThemeStyle,
	readStoredThemeStyle,
	runThemeTransition,
	themeStyleStorageKey,
} from "./theme";

describe("runThemeTransition", () => {
	afterEach(() => {
		Reflect.deleteProperty(document, "startViewTransition");
		delete document.documentElement.dataset.theme;
		delete document.documentElement.dataset.styleTheme;
		document.documentElement.style.colorScheme = "";
	});

	it("runs the update immediately when View Transitions are unavailable", () => {
		Reflect.deleteProperty(document, "startViewTransition");
		const update = vi.fn(() => {
			applyDocumentTheme("light");
		});

		runThemeTransition(update);

		expect(update).toHaveBeenCalledOnce();
		expect(document.documentElement.dataset.theme).toBe("light");
		expect(document.documentElement.style.colorScheme).toBe("light");
	});

	it("wraps the update in startViewTransition when available", () => {
		const startViewTransition = vi.fn((callback: () => void) => {
			callback();
			return {
				finished: Promise.resolve(),
				ready: Promise.resolve(),
				updateCallbackDone: Promise.resolve(),
				skipTransition: () => undefined,
			};
		});
		Object.defineProperty(document, "startViewTransition", {
			configurable: true,
			writable: true,
			value: startViewTransition,
		});
		const update = vi.fn(() => {
			applyDocumentThemeStyle("github");
		});

		runThemeTransition(update);

		expect(startViewTransition).toHaveBeenCalledOnce();
		expect(update).toHaveBeenCalledOnce();
		expect(document.documentElement.dataset.styleTheme).toBe("github");
	});
});

describe("applyDocumentThemeStyle", () => {
	afterEach(() => {
		delete document.documentElement.dataset.styleTheme;
	});

	it("clears data-style-theme for orchestrate", () => {
		document.documentElement.dataset.styleTheme = "nord";
		applyDocumentThemeStyle("orchestrate");
		expect(document.documentElement.dataset.styleTheme).toBeUndefined();
	});

	it("sets data-style-theme for zen", () => {
		applyDocumentThemeStyle("zen");
		expect(document.documentElement.dataset.styleTheme).toBe("zen");
	});
});

describe("readStoredThemeStyle", () => {
	let previous: string | null;

	beforeEach(() => {
		previous = window.localStorage.getItem(themeStyleStorageKey);
	});

	afterEach(() => {
		if (previous === null) {
			window.localStorage.removeItem(themeStyleStorageKey);
		} else {
			window.localStorage.setItem(themeStyleStorageKey, previous);
		}
	});

	it("returns zen when localStorage ao.theme-style is zen", () => {
		window.localStorage.setItem(themeStyleStorageKey, "zen");
		expect(readStoredThemeStyle()).toBe("zen");
	});
});

describe("zen chrome", () => {
	it("is imported by the renderer stylesheet", () => {
		const css = readFileSync(path.resolve(import.meta.dirname, "../styles.css"), "utf8");
		expect(css).toContain("./styles/zen-chrome.css");
	});

	it("keeps Fuji and lantern washes on Zen light only", () => {
		const chrome = readFileSync(path.resolve(import.meta.dirname, "../styles/zen-chrome.css"), "utf8");
		expect(chrome).toContain(':root[data-style-theme="zen"][data-theme="light"] .zen-board-surface::before');
		expect(chrome).toContain(':root[data-style-theme="zen"][data-theme="light"] [data-state="expanded"] [data-sidebar="sidebar"]::after');
		expect(chrome).toContain("調和");
	});
});
