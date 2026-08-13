let meta = #{
    name: "zen-theme",
    description: "Add Zen as a desktop Color Theme (tokens + settings option) and verify",
    when_to_use: "Implement or extend the Agent Orchestrator Color Theme list with the Zen paper/ink/moss skin",
    phases: [
        #{ title: "Survey", detail: "read existing named-theme wiring" },
        #{ title: "Implement", detail: "add zen ThemeStyle, CSS tokens, settings option, tests" },
        #{ title: "Verify", detail: "typecheck, theme tests, and wiring grep" },
    ],
};

let survey_schema = #{
    "type": "object",
    "required": ["files", "notes"],
    "properties": #{
        "files": #{ "type": "array", "items": #{ "type": "string" } },
        "notes": #{ "type": "string" },
    },
};

let implement_schema = #{
    "type": "object",
    "required": ["changed_files", "summary"],
    "properties": #{
        "changed_files": #{ "type": "array", "items": #{ "type": "string" } },
        "summary": #{ "type": "string" },
        "tests_run": #{ "type": "string" },
    },
};

let verify_schema = #{
    "type": "object",
    "required": ["passed", "evidence"],
    "properties": #{
        "passed": #{ "type": "boolean" },
        "evidence": #{ "type": "string" },
        "gaps": #{ "type": "array", "items": #{ "type": "string" } },
    },
};

phase("Survey");
let survey_prompt = "";
survey_prompt += "You are surveying how named Color Themes are wired in Agent Orchestrator. ";
survey_prompt += "Repo root: /Users/tagtaste/Documents/QBApps/agent-orchestrator-roles. ";
survey_prompt += "Use read_file and grep. Do not edit files.\n\n";
survey_prompt += "Read these files in full or the relevant sections:\n";
survey_prompt += "1. frontend/src/renderer/lib/theme.ts\n";
survey_prompt += "2. frontend/src/renderer/lib/theme.test.ts\n";
survey_prompt += "3. frontend/src/renderer/components/settings/GeneralSettingsSection.tsx\n";
survey_prompt += "4. frontend/src/styles/tokens.css — the NAMED THEMES block from the comment ";
survey_prompt += "'NAMED THEMES — applied via data-style-theme' through the end of the solarized light block.\n";
survey_prompt += "5. frontend/src/renderer/lib/terminal-themes.ts (named themes already recolor the terminal plate).\n\n";
survey_prompt += "Grep for ThemeStyle, COLOR_THEME_OPTIONS, data-style-theme, readStoredThemeStyle.\n\n";
survey_prompt += "Return files = every path that must change to add a new named theme named zen, ";
survey_prompt += "following the exact solarized/nord pattern. notes = how a new theme is registered ";
survey_prompt += "(union + storage allow-list + settings option + CSS selectors). ";
survey_prompt += "An empty files list is valid only after you have read the files above.";

let survey = agent(survey_prompt, #{
    label: "survey-theme-wiring",
    capability_mode: "read-only",
    agent_type: "explore",
    output_schema: survey_schema,
});
if survey == () || !survey.success {
    log("Survey failed or returned nothing; implementer will rely on the self-contained spec.");
} else if survey.output.files != () {
    log("Survey noted " + survey.output.files.len().to_string() + " files.");
} else {
    log("Survey returned no file list; implementer will rely on the self-contained spec.");
}

phase("Implement");
let impl_prompt = "";
impl_prompt += "Implement Color Theme option Zen in this repo. Work in the current workspace ";
impl_prompt += "(do not use a separate worktree). Read the files you change before editing.\n\n";
impl_prompt += "LOCKED SCOPE (v1 skin only):\n";
impl_prompt += "- Add zen as a named Color Theme, same mechanism as nord/solarized/gruvbox.\n";
impl_prompt += "- Light + dark token pairs.\n";
impl_prompt += "- Settings → General → Color Theme lists Zen.\n";
impl_prompt += "- Tests for the new style id.\n";
impl_prompt += "- NO layout/IA changes. NO Fuji/bamboo/enso illustrations. NO marketplace JSON. ";
impl_prompt += "NO DESIGN.md rewrite. NO new settings sections. NO role pills on cards.\n";
impl_prompt += "- Do not commit.\n\n";
impl_prompt += "FILES TO CHANGE (and only these unless a test import requires more):\n";
impl_prompt += "1. frontend/src/renderer/lib/theme.ts\n";
impl_prompt += "   - Add | \"zen\" to ThemeStyle after \"solarized\".\n";
impl_prompt += "   - Add stored === \"zen\" to the readStoredThemeStyle allow-list.\n";
impl_prompt += "   - applyDocumentThemeStyle already writes dataset.styleTheme for any non-orchestrate id; do not special-case zen.\n";
impl_prompt += "2. frontend/src/renderer/components/settings/GeneralSettingsSection.tsx\n";
impl_prompt += "   - Append { value: \"zen\", label: \"Zen\" } to COLOR_THEME_OPTIONS.\n";
impl_prompt += "3. frontend/src/styles/tokens.css\n";
impl_prompt += "   - After the solarized light block, add two selectors matching neighboring theme blocks exactly ";
impl_prompt += "(same property set, same comment shape):\n";
impl_prompt += "     :root[data-style-theme=\"zen\"] { ... dark ... }\n";
impl_prompt += "     :root[data-style-theme=\"zen\"][data-theme=\"light\"] { color-scheme: light; ... }\n";
impl_prompt += "   - Also set these ONLY under the zen selectors (not :root):\n";
impl_prompt += "     --color-status-working, --color-status-needs-you, --color-status-in-review, --color-status-ready\n";
impl_prompt += "     so board lane colors match the skin. Leave --color-status-merged as it already follows ready.\n";
impl_prompt += "4. frontend/src/renderer/lib/theme.test.ts\n";
impl_prompt += "   - Add a test that applyDocumentThemeStyle(\"zen\") sets document.documentElement.dataset.styleTheme to \"zen\".\n";
impl_prompt += "   - Add a test that readStoredThemeStyle returns \"zen\" when localStorage ao.theme-style is zen. ";
impl_prompt += "Export/import readStoredThemeStyle if needed. Restore localStorage in afterEach.\n\n";
impl_prompt += "PALETTE — follow the named-theme contrast curve comment in tokens.css ";
impl_prompt += "(dark card +0.055 / muted +0.089 / popover +0.095 / sidebar -0.030 ΔL; ";
impl_prompt += "light card -0.010 / muted -0.022 / popover +0.012 / sidebar +0.012 ΔL).\n\n";
impl_prompt += "Light (primary look from the approved mock):\n";
impl_prompt += "  background #F6F1E8, foreground #2A2620, card #FFFCF6, popover #FBF7F0,\n";
impl_prompt += "  primary #5B7A5A, primary-foreground #F6F1E8, secondary/muted/accent #EFE8DC,\n";
impl_prompt += "  muted-foreground #7A7368, ring/sidebar-primary #5B7A5A, sidebar #F3EEE4,\n";
impl_prompt += "  sidebar-accent #E8E0D2, border oklch(0 0 0 / 10%), input oklch(0 0 0 / 6%).\n";
impl_prompt += "  status: working #4C5A7A, needs-you #C46A3A, in-review #C4A35A, ready #5B7A5A.\n\n";
impl_prompt += "Dark (required pair; warm ink paper, not cool gray):\n";
impl_prompt += "  background #1A1814, foreground #E8E2D6, card #26221C, popover #2C2822,\n";
impl_prompt += "  primary #8FA88A, primary-foreground #1A1814, secondary/muted/accent #2A2620,\n";
impl_prompt += "  muted-foreground #A39B90, ring/sidebar-primary #8FA88A, sidebar #151310,\n";
impl_prompt += "  sidebar-accent #2A2620, border oklch(1 0 0 / 8%), input oklch(1 0 0 / 5%).\n";
impl_prompt += "  status: working #8B9BB8, needs-you #D4845A, in-review #D4B56A, ready #8FA88A.\n\n";
impl_prompt += "Set every token the solarized blocks set (background through sidebar-ring). Do not invent new token names.\n\n";
impl_prompt += "VERIFY BEFORE YOU FINISH:\n";
impl_prompt += "From frontend/: npx vitest run src/renderer/lib/theme.test.ts --reporter=dot\n";
impl_prompt += "From frontend/: npm run typecheck\n";
impl_prompt += "Grep that zen appears in theme.ts, tokens.css, and GeneralSettingsSection.tsx.\n";
impl_prompt += "If a command fails, fix and re-run. Report changed_files, summary, and tests_run output.";

let implemented = agent(impl_prompt, #{
    label: "implement-zen-theme",
    capability_mode: "all",
    agent_type: "orchestrator:developer",
    output_schema: implement_schema,
});
if implemented == () || !implemented.success {
    pause("verification", "Implementer failed. Resume after inspecting the implement-zen-theme agent.");
}

phase("Verify");
let verify_prompt = "";
verify_prompt += "Verify the Zen Color Theme landed in /Users/tagtaste/Documents/QBApps/agent-orchestrator-roles. ";
verify_prompt += "Read-only for source; you MAY run tests. Do not edit files.\n\n";
verify_prompt += "Acceptance (all must be true, with evidence from the tree — not memory):\n";
verify_prompt += "A. theme.ts ThemeStyle includes \"zen\" and readStoredThemeStyle accepts stored === \"zen\".\n";
verify_prompt += "B. GeneralSettingsSection COLOR_THEME_OPTIONS includes { value: \"zen\", label: \"Zen\" }.\n";
verify_prompt += "C. tokens.css has :root[data-style-theme=\"zen\"] and :root[data-style-theme=\"zen\"][data-theme=\"light\"] ";
verify_prompt += "and each sets the same token keys as solarized (background through sidebar-ring).\n";
verify_prompt += "D. Those zen selectors set --color-status-working/needs-you/in-review/ready.\n";
verify_prompt += "E. No Fuji/bamboo illustration assets and no layout/IA changes (sidebar, board columns, settings nav).\n";
verify_prompt += "F. frontend theme tests and typecheck pass:\n";
verify_prompt += "   cd frontend && npx vitest run src/renderer/lib/theme.test.ts --reporter=dot\n";
verify_prompt += "   cd frontend && npm run typecheck\n\n";
verify_prompt += "Set passed=true only if A–F are evidenced. Otherwise passed=false and list gaps. ";
verify_prompt += "evidence must quote file paths and command output.";

let verified = agent(verify_prompt, #{
    label: "verify-zen-theme",
    capability_mode: "execute",
    agent_type: "orchestrator:verifier",
    output_schema: verify_schema,
});

if verified != () && verified.success && verified.output.passed == false {
    log("Verifier rejected the first landing; sending one fix pass.");
    let fix_prompt = "";
    fix_prompt += "The Zen Color Theme verification failed. Fix only the gaps below, then re-run ";
    fix_prompt += "frontend theme tests and typecheck. Do not expand scope.\n\nGaps:\n";
    if verified.output.gaps != () {
        let gi = 0;
        for g in verified.output.gaps {
            gi += 1;
            fix_prompt += gi.to_string() + ". " + g + "\n";
        }
    }
    fix_prompt += "\nVerifier evidence:\n" + verified.output.evidence;
    let fixed = agent(fix_prompt, #{
        label: "fix-zen-theme",
        capability_mode: "all",
        agent_type: "orchestrator:developer",
        resume_from: implemented.agent_id,
        output_schema: implement_schema,
    });
    if fixed == () || !fixed.success {
        pause("verification", "Fix pass failed. Resume after inspecting fix-zen-theme.");
    }
    let re_verify_prompt = verify_prompt;
    re_verify_prompt += "\nThis is a re-check after a fix pass. Re-read the files and re-run the commands.";
    let rechecked = agent(re_verify_prompt, #{
        label: "reverify-zen-theme",
        capability_mode: "execute",
        agent_type: "orchestrator:verifier",
        output_schema: verify_schema,
    });
    if rechecked == () || !rechecked.success || rechecked.output.passed != true {
        let ev = "";
        if rechecked != () && rechecked.success { ev = rechecked.output.evidence; }
        write_scratch_file("zen-theme-report.md", "Zen theme verification still failing.\n\n" + ev);
        pause("verification", "Zen theme still fails acceptance after one fix pass. Inspect evidence, then resume or stop.");
    }
}

let report = "# Zen Color Theme\n\n";
if implemented != () && implemented.success {
    if implemented.output.summary != () {
        report += implemented.output.summary + "\n\n";
    }
    if implemented.output.changed_files != () {
        report += "Files:\n";
        for f in implemented.output.changed_files {
            report += "- " + f + "\n";
        }
    }
}
if verified != () && verified.success {
    if verified.output.passed != () {
        report += "\nVerifier: passed=" + verified.output.passed.to_string() + "\n\n";
    }
    if verified.output.evidence != () {
        report += verified.output.evidence;
    }
}
let report_path = write_scratch_file("zen-theme-report.md", report);
complete(#{
    summary: "Zen Color Theme implemented and verified",
    report: report_path,
});
