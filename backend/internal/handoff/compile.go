// Package handoff compiles SemanticHandoffV1 + ObservedWorkspaceV1 into a
// host-authoritative target prompt fragment. Observed Git/test facts always
// override agent semantic claims. No I/O.
package handoff

import (
	"fmt"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// CompileInput is the host-side inputs for a switch or fresh-conversation handoff.
type CompileInput struct {
	Semantic         domain.SemanticHandoffV1
	Observed         domain.ObservedWorkspaceV1
	RoleID           string
	TargetGeneration string
	// SameHarness is true for fresh-conversation (provider unchanged).
	SameHarness bool
	FromHarness domain.AgentHarness
	ToHarness   domain.AgentHarness
}

// Compile merges observed (authoritative) and semantic (untrusted) into
// CompiledHandoff.Text for the target agent launch.
func Compile(in CompileInput) domain.CompiledHandoff {
	sem := in.Semantic
	if sem.SchemaVersion == 0 {
		sem.SchemaVersion = domain.SemanticHandoffSchemaVersion
	}
	obs := in.Observed
	if obs.SchemaVersion == 0 {
		obs.SchemaVersion = domain.ObservedWorkspaceSchemaVersion
	}

	var b strings.Builder
	b.WriteString("## Host-compiled handoff (authoritative workspace facts override agent claims)\n\n")
	if in.SameHarness {
		fmt.Fprintf(&b, "Kind: same-harness fresh conversation (`%s`).\n", in.FromHarness)
	} else {
		fmt.Fprintf(&b, "Kind: provider switch `%s` → `%s`.\n", in.FromHarness, in.ToHarness)
	}
	if rid := strings.TrimSpace(in.RoleID); rid != "" {
		fmt.Fprintf(&b, "Role id (preserved): `%s`.\n", rid)
	}
	if g := strings.TrimSpace(in.TargetGeneration); g != "" {
		fmt.Fprintf(&b, "Target generation (owns input after ack): `%s`.\n", g)
	}
	if g := strings.TrimSpace(sem.SourceGeneration); g != "" {
		fmt.Fprintf(&b, "Source generation: `%s`.\n", g)
	}
	b.WriteString("\n### Observed workspace (host)\n")
	writeObserved(&b, obs)
	b.WriteString("\n### Semantic context (agent-authored; untrusted for git/tests)\n")
	writeSemantic(&b, sem, obs)
	b.WriteString("\n### Compiler rules\n")
	b.WriteString("- Prefer **Observed** branch/HEAD/porcelain over any agent claim.\n")
	b.WriteString("- Treat agent-reported test results as **claims** unless listed under Verified results.\n")
	b.WriteString("- Continue the role objective; do not re-litigate rejected approaches unless the user asks.\n")

	return domain.CompiledHandoff{
		Text:             strings.TrimSpace(b.String()),
		RoleID:           strings.TrimSpace(in.RoleID),
		SourceGeneration: strings.TrimSpace(sem.SourceGeneration),
		TargetGeneration: strings.TrimSpace(in.TargetGeneration),
		Semantic:         sem,
		Observed:         obs,
	}
}

func writeObserved(b *strings.Builder, obs domain.ObservedWorkspaceV1) {
	if obs.Branch != "" {
		fmt.Fprintf(b, "- Branch: `%s`\n", obs.Branch)
	}
	if obs.Head != "" {
		fmt.Fprintf(b, "- HEAD: `%s`\n", obs.Head)
	}
	if obs.Worktree != "" {
		fmt.Fprintf(b, "- Worktree: `%s`\n", obs.Worktree)
	}
	if strings.TrimSpace(obs.Porcelain) != "" {
		fmt.Fprintf(b, "- Status (porcelain):\n```\n%s\n```\n", strings.TrimSpace(obs.Porcelain))
	}
	if len(obs.SHAs) > 0 {
		b.WriteString("- SHAs:\n")
		for k, v := range obs.SHAs {
			fmt.Fprintf(b, "  - %s: `%s`\n", k, v)
		}
	}
	if !obs.ObservedAt.IsZero() {
		fmt.Fprintf(b, "- Observed at: %s\n", obs.ObservedAt.UTC().Format(time.RFC3339))
	}
	if len(obs.VerifiedResults) == 0 {
		b.WriteString("- Verified results: *(none — re-run tests if needed)*\n")
		return
	}
	b.WriteString("- Verified results (AO-captured only):\n")
	for _, r := range obs.VerifiedResults {
		fmt.Fprintf(b, "  - `%s` exit=%d %s\n", r.Command, r.ExitCode, r.Summary)
	}
}

func writeSemantic(b *strings.Builder, sem domain.SemanticHandoffV1, obs domain.ObservedWorkspaceV1) {
	if sem.Objective != "" {
		fmt.Fprintf(b, "- Objective: %s\n", sem.Objective)
	}
	if sem.LatestUserIntent != "" {
		fmt.Fprintf(b, "- Latest user intent: %s\n", sem.LatestUserIntent)
	}
	writeList(b, "Open items", sem.OpenItems)
	writeList(b, "Claimed decisions", sem.ClaimedDecisions)
	writeList(b, "Rejected approaches", sem.RejectedApproaches)
	writeList(b, "Uncertainty", sem.Uncertainty)
	if sem.NativeSessionID != "" {
		fmt.Fprintf(b, "- Source native session: `%s`\n", sem.NativeSessionID)
	}
	// Explicitly note that agent git claims are discarded when observed is present.
	if obs.Head != "" || obs.Branch != "" {
		b.WriteString("- *(Agent git claims omitted — host Observed section is authoritative.)*\n")
	}
}

func writeList(b *strings.Builder, title string, items []string) {
	if len(items) == 0 {
		return
	}
	fmt.Fprintf(b, "- %s:\n", title)
	for _, it := range items {
		it = strings.TrimSpace(it)
		if it == "" {
			continue
		}
		fmt.Fprintf(b, "  - %s\n", it)
	}
}
