// web/scripts/check-lines.ts
import fs from "node:fs";
import path from "node:path";

const MAX_LINES = 800;

// Static SVG icon lookup tables explicitly exempted by AGENTS.md Rule #1
const EXEMPT_SUFFIXES = [
  "web/src/components/icons/file-icons/types.ts",
  "web/src/components/icons/file-icons/icon-maps.ts",
];

// Read-only services/ calls permitted directly in components/ (all writes must go through store/).
const COMPONENT_SERVICE_READONLY_ALLOWLIST: Record<
  string,
  { allowed: string[]; reason: string }
> = {
  "web/src/components/handoff/ExternalHandoffModal.tsx": {
    allowed: [
      "hoxiAPI.getExternalSessionPreview",
      "hoxiAPI.listExternalSessions",
      "hoxiAPI.getLocalCLIStatus",
    ],
    reason: "Read-only external CLI session discovery and preview before handoff import",
  },
  "web/src/components/inspector/FileTreeNodes.tsx": {
    allowed: ["hoxiAPI.listFiles"],
    reason: "Read-only lazy directory listing when expanding file tree nodes",
  },
  "web/src/components/inspector/FileTreeView.tsx": {
    allowed: ["hoxiAPI.listFiles", "hoxiAPI.findFiles", "hoxiAPI.readFileContent"],
    reason: "Read-only workspace file tree browsing, search, and file preview content loading",
  },
  "web/src/components/inspector/MemoryBankView.tsx": {
    allowed: ["hoxiAPI.readFileContent"],
    reason: "Read-only loading of .opencode/memory markdown files in the Memory inspector",
  },
  "web/src/components/inspector/PullRequestBadge.tsx": {
    allowed: ["fetchPullRequest"],
    reason: "Read-only GitHub/GitLab pull request status badge lookup",
  },
  "web/src/components/inspector/PullRequestIssuesButton.tsx": {
    allowed: ["fetchPullRequestDetail"],
    reason: "Read-only GitHub/GitLab pull request review thread and check details lookup",
  },
  "web/src/components/inspector/pierre-diff-parts.tsx": {
    allowed: ["hoxiAPI.getWorktreeFileDiff"],
    reason: "Read-only on-demand full file diff fetch when expanding a file in the diff viewer",
  },
  "web/src/components/layout/CommandPalette.tsx": {
    allowed: ["hoxiAPI.searchFiles"],
    reason: "Read-only fuzzy file search for the command palette",
  },
  "web/src/components/layout/LeftNav.tsx": {
    allowed: ["hoxiAPI.listSessions"],
    reason: "Read-only session count probe when confirming project removal",
  },
  "web/src/components/layout/SessionNavItem.tsx": {
    allowed: ["fetchPullRequests"],
    reason: "Read-only branch pull request badge lookup for sidebar session items",
  },
  "web/src/components/layout/WorkspaceModal.tsx": {
    allowed: ["hoxiAPI.openDirectoryDialog"],
    reason: "Read-only native OS folder picker dialog returning a selected directory path",
  },
  "web/src/components/models/CustomEndpointTab.tsx": {
    allowed: ["hoxiAPI.fetchCustomProviderModels"],
    reason: "Read-only /v1/models discovery probe for populating the custom provider form draft",
  },
  "web/src/components/models/LocalCliBridgeBanner.tsx": {
    allowed: ["hoxiAPI.getLocalCLIStatus"],
    reason: "Read-only local CLI installation and credential status probe for the bridge banner",
  },
  "web/src/components/terminal/WebGLTerminal.tsx": {
    allowed: ["getGatewayToken"],
    reason: "Read-only gateway auth token accessor for constructing the PTY WebSocket URL",
  },
  "web/src/components/timeline/Composer.tsx": {
    allowed: ["hoxiAPI.openDirectoryDialog", "hoxiAPI.enhancePrompt"],
    reason:
      "Read-only native folder picker dialog and stateless prompt enhancement returning text for composer input",
  },
  "web/src/components/timeline/MarkdownImage.tsx": {
    allowed: ["hoxiAPI.readFileContent"],
    reason: "Read-only workspace image file loader for rendering relative markdown images",
  },
  "web/src/components/timeline/composer-autocomplete.ts": {
    allowed: [
      "hoxiAPI.listCommands",
      "hoxiAPI.findFiles",
      "hoxiAPI.listFiles",
      "skillsAPI.fetchSkillsList",
    ],
    reason: "Read-only slash command, skill, and @file autocomplete candidate queries",
  },
  "web/src/components/timeline/composer-hooks.ts": {
    allowed: ["hoxiAPI.readClipboardImages"],
    reason: "Read-only native clipboard image reader for Cmd+V paste in the composer",
  },
  "web/src/components/timeline/session-export.ts": {
    allowed: ["hoxiAPI.getMessages"],
    reason: "Read-only full session message history fetch when exporting a transcript to Markdown",
  },
};

function extractRuntimeServiceImports(content: string): {
  hasRuntimeImportStmt: boolean;
  runtimeSymbols: string[];
} {
  const re =
    /(?:^|\n)\s*import\s+(?!type\b)([^;]+?)\s+from\s+["'][^"']*\/services\/[^"']+["']\s*;?/g;
  let hasRuntimeImportStmt = false;
  const runtimeSymbols: string[] = [];
  let match: RegExpExecArray | null;
  while ((match = re.exec(content)) !== null) {
    hasRuntimeImportStmt = true;
    const clause = match[1].trim();
    const braceMatch = clause.match(/\{([\s\S]*?)\}/);
    if (braceMatch) {
      for (const rawPart of braceMatch[1].split(",")) {
        const part = rawPart.trim();
        if (!part || /^type\s+/.test(part)) continue;
        const localName = part.split(/\s+as\s+/).pop()?.trim();
        if (localName) runtimeSymbols.push(localName);
      }
    }
    const outsideBraces = clause.replace(/\{[\s\S]*?\}/, "").replace(/,/g, "").trim();
    if (outsideBraces && !/^type\s+/.test(outsideBraces)) {
      const nsMatch = outsideBraces.match(/\*\s+as\s+([A-Za-z_$][A-Za-z0-9_$]*)/);
      if (nsMatch) {
        runtimeSymbols.push(nsMatch[1]);
      } else {
        runtimeSymbols.push(outsideBraces);
      }
    }
  }
  return { hasRuntimeImportStmt, runtimeSymbols };
}

const IGNORE_DIRS = new Set([
  ".git",
  ".hoxi",
  ".claude",
  ".dart_tool",
  ".pub-cache",
  "node_modules",
  ".pnpm-store",
  "vendor",
  "dist",
  "build",
  "wailsjs",
  "Pods",
  "DerivedData",
  "ephemeral",
  "go-build",
  "go-build-cache",
  ".gocache",
  ".gomodcache",
]);

const GO_BASE_PACKAGES = new Set([
  "agentcli",
  "agyhook",
  "buildinfo",
  "claudeconfig",
  "diagnostics",
  "hoxipaths",
  "llm",
  "notify",
  "opencode/ocapi",
  "opencode/ocstate",
  "statusbar",
  "textindex",
]);

const GO_ALLOWED_DOMAIN_IMPORTS: Record<string, string[]> = {
  opencode: ["sandbox"],
  provider: ["skills"],
  runner: ["attachment", "sandbox", "worktree"],
  telemetry: ["auth"],
};

const SOURCE_EXT = /\.(go|ts|tsx|js|m|dart)$/;

function walkRepo(dir: string): string[] {
  let results: string[] = [];
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    if (IGNORE_DIRS.has(entry.name)) continue;
    const fullPath = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      results = results.concat(walkRepo(fullPath));
    } else if (SOURCE_EXT.test(entry.name)) {
      results.push(fullPath);
    }
  }
  return results;
}

const repoRoot = fs.existsSync("./src") ? path.resolve("..") : path.resolve(".");
const files = walkRepo(repoRoot);

let violations = 0;
const seenAllowlistFiles = new Set<string>();

for (const file of files) {
  const rel = path.relative(repoRoot, file).replace(/\\/g, "/");
  if (EXEMPT_SUFFIXES.some((s) => rel.endsWith(s))) continue;

  const content = fs.readFileSync(file, "utf8");
  const lineCount = content.endsWith("\n")
    ? content.split("\n").length - 1
    : content.split("\n").length;

  if (lineCount > MAX_LINES) {
    console.error(`  ✗ [${MAX_LINES}-line limit] ${rel}: ${lineCount} lines (max ${MAX_LINES})`);
    violations++;
  }

  const baseName = path.basename(rel);
  if (/^zz_scratch_/.test(baseName)) {
    console.error(
      `  ✗ [scratch file violation] ${rel} must be converted to a permanent regression test or removed before finishing.`
    );
    violations++;
  }

  // Enforce unidirectional frontend layering & naming: components -> store -> services
  if (rel.startsWith("web/src/services/")) {
    if (!/^[a-z0-9-]+\.ts$/.test(baseName)) {
      console.error(`  ✗ [naming violation] ${rel} must use lowercase kebab-case.ts filename.`);
      violations++;
    }
    const badImport = content.match(/(?:from\s+|import\s*\(\s*)["'][^"']*\/(?:store|components)(?:\/|["'])/);
    if (badImport) {
      console.error(`  ✗ [layering violation] ${rel} must not import from store/ or components/ (${badImport[0]}). Move state/UI logic to web/src/store/ or web/src/components/.`);
      violations++;
    }
    if (/["'`]\/(?:session|event|permission)(?:[/"'`?]|$)/.test(content)) {
      console.error(`  ✗ [gateway plane violation] ${rel} must not call raw '/session', '/event', or '/permission'; route through '/api/hoxi/sessions', '/api/hoxi/event', and '/api/hoxi/permission'.`);
      violations++;
    }
  } else if (rel.startsWith("web/src/store/")) {
    if (!/^[a-z0-9-]+\.ts$/.test(baseName)) {
      console.error(`  ✗ [naming violation] ${rel} must use lowercase kebab-case.ts filename.`);
      violations++;
    }
    const badImport = content.match(/(?:from\s+|import\s*\(\s*)["'][^"']*\/components(?:\/|["'])/);
    if (badImport) {
      console.error(`  ✗ [layering violation] ${rel} must not import from components/ (${badImport[0]}). Move pure domain helpers into web/src/store/.`);
      violations++;
    }
    if (rel !== "web/src/store/workspace.ts" && /^\s*export\s+\*\s+from\s+/m.test(content)) {
      console.error(`  ✗ [barrel violation] ${rel} must not use secondary 'export * from' (re-export flatly in web/src/store/workspace.ts instead).`);
      violations++;
    }
    if (rel.endsWith("-storage.ts")) {
      console.error(`  ✗ [fragment violation] ${rel} must not split out a separate *-storage.ts scaffold file; keep persistence inside its domain store module.`);
      violations++;
    }
    if (/\bEvents(?:On|Off)\s*\(|\bwindow\b[^\n]*\.runtime\b/.test(content)) {
      console.error(`  ✗ [layering violation] ${rel} must not access Wails window.runtime or EventsOn/EventsOff directly in store/; wrap host runtime bridges in web/src/services/.`);
      violations++;
    }
  } else if (rel.startsWith("web/src/components/")) {
    if (/\blocalStorage\b/.test(content)) {
      console.error(`  ✗ [layering violation] ${rel} must not access localStorage directly in components/; move persistence into web/src/store/.`);
      violations++;
    }
    if (/\bEvents(?:On|Off)\s*\(|\bwindow\b[^\n]*\.runtime\b/.test(content)) {
      console.error(`  ✗ [layering violation] ${rel} must not access Wails window.runtime or EventsOn/EventsOff directly in components/; route through web/src/services/ and web/src/store/.`);
      violations++;
    }
    const { hasRuntimeImportStmt, runtimeSymbols } = extractRuntimeServiceImports(content);
    const allowEntry = COMPONENT_SERVICE_READONLY_ALLOWLIST[rel];
    if (hasRuntimeImportStmt) {
      if (!allowEntry) {
        console.error(
          `  ✗ [layering violation] ${rel} must only 'import type' from services/ (route state/writes through web/src/store/, or register read-only queries in COMPONENT_SERVICE_READONLY_ALLOWLIST).`
        );
        violations++;
      } else {
        seenAllowlistFiles.add(rel);
        const bodyOnly = content.replace(
          /(?:^|\n)\s*import\s+[^;]+?from\s+["'][^"']+["']\s*;?/g,
          ""
        );
        const usedMembers = new Set<string>();
        for (const sym of runtimeSymbols) {
          const propRe = new RegExp(`\\b${sym}(?:\\s*\\.\\s*([A-Za-z_$][A-Za-z0-9_$]*))?`, "g");
          let pm: RegExpExecArray | null;
          while ((pm = propRe.exec(bodyOnly)) !== null) {
            usedMembers.add(pm[1] ? `${sym}.${pm[1]}` : sym);
          }
        }
        for (const used of usedMembers) {
          if (!allowEntry.allowed.includes(used)) {
            console.error(
              `  ✗ [layering violation] ${rel} uses non-whitelisted services member '${used}' (allowed: ${allowEntry.allowed.join(", ")}). Route writes through web/src/store/.`
            );
            violations++;
          }
        }
        for (const allowedMember of allowEntry.allowed) {
          if (!usedMembers.has(allowedMember)) {
            console.error(
              `  ✗ [stale allowlist] ${rel} no longer uses whitelisted services member '${allowedMember}'; update COMPONENT_SERVICE_READONLY_ALLOWLIST in web/scripts/check-lines.ts.`
            );
            violations++;
          }
        }
      }
    }
  } else if (rel.startsWith("web/src/i18n/") && /domain-.*-types\.ts$/.test(rel)) {
    console.error(`  ✗ [i18n violation] ${rel} must not define manual i18n interface files; types are automatically inferred from en-US in web/src/i18n/types.ts.`);
    violations++;
  } else if (rel.startsWith("internal/") && rel.endsWith(".go")) {
    if (!/^[a-z0-9_]+\.go$/.test(baseName)) {
      console.error(`  ✗ [naming violation] ${rel} must use lowercase snake_case.go filename.`);
      violations++;
    }
    if (/type\s+desktopGateway\s*=/.test(content)) {
      console.error(`  ✗ [boundary violation] ${rel} must not define legacy 'type desktopGateway =' alias; use explicit package receiver (*Service / *Gateway).`);
      violations++;
    }
    if (
      !rel.startsWith("internal/app/") &&
      !rel.startsWith("internal/cli/") &&
      !rel.startsWith("internal/gateway/") &&
      !rel.endsWith("_test.go") &&
      /"github\.com\/hoxiai\/hoxi\/internal\/(?:gateway|app|cli)"/.test(content)
    ) {
      console.error(`  ✗ [layering violation] ${rel} must not reverse-import internal/gateway, internal/app, or internal/cli.`);
      violations++;
    }
    if (
      !rel.startsWith("internal/app/") &&
      !rel.startsWith("internal/cli/") &&
      !rel.startsWith("internal/gateway/") &&
      !rel.startsWith("internal/provider/") &&
      !rel.endsWith("_test.go") &&
      /"github\.com\/hoxiai\/hoxi\/internal\/provider"/.test(content)
    ) {
      console.error(`  ✗ [layering violation] ${rel} must not import internal/provider directly; inject llm.Client from internal/gateway.`);
      violations++;
    }
    if (
      rel === "internal/gateway/tunnel.go" &&
      /\btunnelRoutePrefixes\b/.test(content)
    ) {
      console.error(`  ✗ [tunnel guard violation] ${rel} must use explicit (method, pattern) tunnelRoutes, never prefix-based tunnelRoutePrefixes.`);
      violations++;
    }
    if (!rel.endsWith("_test.go")) {
      const pkgDir = path.posix.dirname(rel).replace(/^internal\//, "");
      const internalImports = [
        ...content.matchAll(/"github\.com\/hoxiai\/hoxi\/internal\/([^"]+)"/g),
      ].map((m) => m[1]);
      if (GO_BASE_PACKAGES.has(pkgDir)) {
        for (const imp of internalImports) {
          if (!GO_BASE_PACKAGES.has(imp)) {
            console.error(
              `  ✗ [layering violation] base package ${rel} (${pkgDir}) must not import non-base package internal/${imp}.`
            );
            violations++;
          }
        }
      } else if (pkgDir !== "app" && pkgDir !== "cli" && pkgDir !== "gateway") {
        const allowedDomains = new Set(GO_ALLOWED_DOMAIN_IMPORTS[pkgDir] || []);
        for (const imp of internalImports) {
          if (imp !== pkgDir && !GO_BASE_PACKAGES.has(imp) && !allowedDomains.has(imp)) {
            console.error(
              `  ✗ [layering violation] domain package ${rel} (${pkgDir}) must not cross-import domain package internal/${imp}; wire cross-domain coordination in internal/gateway.`
            );
            violations++;
          }
        }
      }
    }
    if (
      !rel.startsWith("internal/provider/") &&
      !rel.endsWith("_test.go") &&
      rel !== "internal/handoff/grok_bridge.go" &&
      /["']\/(?:v1\/messages|chat\/completions)["']/.test(content)
    ) {
      console.error(`  ✗ [boundary violation] ${rel} must not hand-roll '/v1/messages' or '/chat/completions' requests; reuse callSimpleLLM in internal/provider/http.go.`);
      violations++;
    }
  } else if (rel.startsWith("client/lib/") && rel.endsWith(".dart")) {
    if (!/^[a-z0-9_]+\.dart$/.test(baseName)) {
      console.error(`  ✗ [naming violation] ${rel} must use lowercase snake_case.dart filename.`);
      violations++;
    }
  }
}

for (const [allowFile, allowEntry] of Object.entries(COMPONENT_SERVICE_READONLY_ALLOWLIST)) {
  if (!allowEntry.reason.trim()) {
    console.error(`  ✗ [allowlist violation] ${allowFile} is missing a reason in COMPONENT_SERVICE_READONLY_ALLOWLIST.`);
    violations++;
  }
  if (!seenAllowlistFiles.has(allowFile)) {
    console.error(
      `  ✗ [stale allowlist] ${allowFile} no longer has runtime imports from services/; remove it from COMPONENT_SERVICE_READONLY_ALLOWLIST in web/scripts/check-lines.ts.`
    );
    violations++;
  }
}

if (violations > 0) {
  console.error(`\n[lint:lines] ✗ ${violations} architectural or line-limit violation(s) found.`);
  process.exit(1);
} else {
  console.log(`[lint:lines] ✓ All ${files.length} source files comply with the ${MAX_LINES}-line limit and layering boundaries.`);
}
