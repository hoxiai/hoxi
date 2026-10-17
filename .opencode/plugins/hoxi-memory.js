// .opencode/plugins/hoxi-memory.js
import fs from "node:fs/promises";
import path from "node:path";

const EIGHT_DIM_COMPACTION_PROMPT = `You are compacting an engineering session for hoxi.
Summarize the conversation into the following 8 mandatory dimensions in concise Markdown:
1. ## Goals (Core user objectives and success criteria)
2. ## Constraints (Architectural boundaries, forbidden actions, user rules)
3. ## Key Facts (Discovered file paths, APIs, ports, schemas, environment details)
4. ## Changed Files (Modified files and the exact rationale for each change)
5. ## Errors & Fixes (Root causes of bugs encountered and how they were resolved)
6. ## Todo (Remaining unfinished tasks)
7. ## Progress (Verified milestones completed so far)
8. ## Next Steps (Immediate actionable steps for the next turn)`;

export const HoxiMemoryPlugin = async ({ directory }) => {
  const memoryDir = path.join(directory, ".opencode", "memory");
  const projectMemoryPath = path.join(memoryDir, "project.md");
  const sessionsMemoryDir = path.join(memoryDir, "sessions");

  return {
    "experimental.chat.system.transform": async (_input, output) => {
      try {
        const content = await fs.readFile(projectMemoryPath, "utf8");
        if (content.trim()) {
          output.system.push(`<project_memory>\n${content.trim()}\n</project_memory>`);
        }
      } catch {
        // Ignore if project.md does not exist yet
      }
    },

    "experimental.session.compacting": async (_input, output) => {
      output.prompt = EIGHT_DIM_COMPACTION_PROMPT;
    },

    event: async ({ event }) => {
      if (event.type === "session.idle") {
        const sessionID = event.properties?.sessionID || "unknown";
        await fs.mkdir(sessionsMemoryDir, { recursive: true });
        const sessionFile = path.join(sessionsMemoryDir, `${sessionID}.md`);
        try {
          await fs.access(sessionFile);
        } catch {
          const template = [
            `# Session Memory (${sessionID})`,
            `Updated: ${new Date().toISOString()}`,
            "",
            "## 1. Goals",
            "## 2. Constraints",
            "## 3. Key Facts",
            "## 4. Changed Files",
            "## 5. Errors & Fixes",
            "## 6. Todo",
            "## 7. Progress",
            "## 8. Next Steps",
            "",
          ].join("\n");
          await fs.writeFile(sessionFile, template, "utf8");
        }
      }
    },
  };
};

export default HoxiMemoryPlugin;
