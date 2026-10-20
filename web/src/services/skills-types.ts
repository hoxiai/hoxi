// web/src/services/skills-types.ts

export type SkillSource = "builtin" | "project" | "user" | "external";

export interface SkillDescriptor {
  key: string;
  name: string;
  description?: string;
  runtime: "opencode";
  source: SkillSource;
  sourceLabel: string;
  canOpenLocation: boolean;
  contentDigest?: string;
}

export interface SkillDetail extends SkillDescriptor {
  content: string;
  location?: string;
}

export interface SkillSelection {
  key: string;
  name: string;
}

export interface SkillListResponse {
  runtime: string;
  supported: boolean;
  directory: string;
  sessionID?: string;
  skills: SkillDescriptor[];
  unsupportedReason?: string;
  cacheHint?: string;
}
