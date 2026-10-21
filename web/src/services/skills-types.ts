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

export interface MarketplaceSkill {
  slug: string;
  name: string;
  description?: string;
  category?: string;
  categoryLabel?: string;
  author?: string;
  version?: string;
  runtime?: string;
  contentUrl: string;
  contentSha256: string;
  homepageUrl?: string;
  repoUrl?: string;
  badge?: string;
  badgeTone?: string;
  icon?: string;
  compatibility?: string;
  highlights?: string[];
  order?: number;
  installed: boolean;
  installedDigest?: string;
  updateAvailable?: boolean;
}

export interface MarketplaceSkillListResponse {
  skills: MarketplaceSkill[];
  source: string;
  cacheHint?: string;
}

export interface MarketplaceInstallResponse {
  ok: boolean;
  skill: MarketplaceSkill;
  reload: boolean;
}
