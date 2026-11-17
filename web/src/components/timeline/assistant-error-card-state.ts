// web/src/components/timeline/assistant-error-card-state.ts
import { formatMemberPoints } from "../../store/workspace";
import { t } from "../../i18n";

export type ErrorCardKind =
  | "stall"
  | "overflow"
  | "model"
  | "officialBalance"
  | "providerBalance"
  | "api";

export type ErrorCardActionID =
  | "compactRetry"
  | "reloadAndRetry"
  | "reloadCatalog"
  | "checkin"
  | "topup"
  | "balanceRetry"
  | "retryModel"
  | "retryStall"
  | "fallback"
  | "configure"
  | "diagnostics";

export interface ErrorCardActionItem {
  id: ErrorCardActionID;
  primary: boolean;
}

export interface ErrorCardViewInput {
  kind: ErrorCardKind;
  superseded: boolean;
  hasSelectedModel: boolean;
  hasFallbackModel: boolean;
  isAuthError?: boolean;
  isProviderDegraded?: boolean;
  configOpened?: boolean;
  rawErrorName?: string;
  rawErrorMessage?: string;
  memberLoaded?: boolean;
  points?: number;
  pointsIncreased?: boolean;
  canCheckin?: boolean;
  checkinError?: boolean;
  topUpInitiated?: boolean;
}

export interface ErrorCardViewState {
  tone: "notice" | "danger";
  title: string;
  message: string;
  monoMessage: boolean;
  showStatusCode: boolean;
  showHealthBadge: boolean;
  actions: ErrorCardActionItem[];
}

/**
 * Pure presentation state resolver across all 6 assistant error branches:
 * - State/sync conditions (overflow, model not loaded, stall, balance) use a calm
 *   notice surface, human sans-serif copy, and hide raw status codes & irrelevant chrome.
 * - True upstream API failures retain danger styling and monospace raw error text,
 *   while keeping primary recovery leftmost and diagnostics last.
 */
export function resolveErrorCardViewState(input: ErrorCardViewInput): ErrorCardViewState {
  switch (input.kind) {
    case "overflow":
      return {
        tone: "notice",
        title: t("timeline.contextOverflowTitle"),
        message: t("timeline.contextOverflowMessage"),
        monoMessage: false,
        showStatusCode: false,
        showHealthBadge: false,
        actions: input.superseded ? [] : [{ id: "compactRetry", primary: true }],
      };

    case "model": {
      const actions: ErrorCardActionItem[] = [
        {
          id: !input.superseded && input.hasSelectedModel ? "reloadAndRetry" : "reloadCatalog",
          primary: true,
        },
      ];
      if (!input.superseded && input.hasFallbackModel) {
        actions.push({ id: "fallback", primary: false });
      } else if (!input.superseded) {
        actions.push({ id: "configure", primary: false });
      }
      return {
        tone: "notice",
        title: t("timeline.modelNotLoadedTitle"),
        message: t("timeline.modelNotLoadedMessage"),
        monoMessage: false,
        showStatusCode: false,
        showHealthBadge: false,
        actions,
      };
    }

    case "stall": {
      const actions: ErrorCardActionItem[] = [];
      if (!input.superseded && input.hasSelectedModel) {
        actions.push({ id: "retryStall", primary: true });
      }
      actions.push({ id: "diagnostics", primary: false });
      return {
        tone: "notice",
        title: t("timeline.stalledErrorTitle"),
        message: t("timeline.stalledErrorMessage"),
        monoMessage: false,
        showStatusCode: false,
        showHealthBadge: false,
        actions,
      };
    }

    case "officialBalance": {
      const formatted = formatMemberPoints(input.points ?? 0);
      const message = input.memberLoaded
        ? input.pointsIncreased
          ? t("timeline.balanceOfficialUpdatedMessage", { points: formatted })
          : t("timeline.balanceOfficialPointsMessage", { points: formatted })
        : t("timeline.balanceOfficialMessage");

      const readyToRetry =
        !input.superseded &&
        input.hasSelectedModel &&
        Boolean(input.topUpInitiated || input.pointsIncreased);

      const actions: ErrorCardActionItem[] = [];
      if (readyToRetry) {
        actions.push({ id: "balanceRetry", primary: true });
        actions.push({ id: "topup", primary: false });
      } else if (input.canCheckin) {
        actions.push({ id: "checkin", primary: true });
        if (input.checkinError) {
          actions.push({ id: "topup", primary: false });
        }
      } else {
        actions.push({ id: "topup", primary: true });
      }

      if (!input.superseded) {
        actions.push({
          id: input.hasFallbackModel ? "fallback" : "configure",
          primary: false,
        });
      }

      return {
        tone: "notice",
        title: t("timeline.balanceOfficialTitle"),
        message,
        monoMessage: false,
        showStatusCode: false,
        showHealthBadge: false,
        actions,
      };
    }

    case "providerBalance": {
      const actions: ErrorCardActionItem[] = [];
      if (!input.superseded && input.hasFallbackModel) {
        actions.push({ id: "fallback", primary: true });
        if (input.hasSelectedModel) {
          actions.push({ id: "balanceRetry", primary: false });
        }
        actions.push({ id: "configure", primary: false });
      } else {
        actions.push({ id: "configure", primary: true });
        if (!input.superseded && input.hasSelectedModel) {
          actions.push({ id: "balanceRetry", primary: false });
        }
      }
      return {
        tone: "notice",
        title: t("timeline.balanceTitle"),
        message: t("timeline.balanceMessage"),
        monoMessage: false,
        showStatusCode: false,
        showHealthBadge: false,
        actions,
      };
    }

    case "api": {
      const hasRaw = Boolean(input.rawErrorMessage || input.rawErrorName);
      const message =
        input.rawErrorMessage || input.rawErrorName || t("timeline.modelRequestFailed");
      const actions: ErrorCardActionItem[] = [];

      if (input.isAuthError) {
        if (input.configOpened && !input.superseded && input.hasSelectedModel) {
          actions.push({ id: "retryModel", primary: true });
          actions.push({ id: "configure", primary: false });
        } else {
          actions.push({ id: "configure", primary: !input.superseded });
        }
        if (!input.superseded && input.hasFallbackModel) {
          actions.push({ id: "fallback", primary: false });
        }
        actions.push({ id: "diagnostics", primary: false });
      } else if (!input.superseded) {
        if (input.isProviderDegraded && input.hasFallbackModel) {
          actions.push({ id: "fallback", primary: true });
          if (input.hasSelectedModel) {
            actions.push({ id: "retryModel", primary: false });
          }
        } else {
          if (input.hasSelectedModel) {
            actions.push({ id: "retryModel", primary: true });
          }
          if (input.hasFallbackModel) {
            actions.push({ id: "fallback", primary: !input.hasSelectedModel });
          } else {
            actions.push({ id: "configure", primary: !input.hasSelectedModel });
          }
        }
        actions.push({ id: "diagnostics", primary: false });
      } else {
        actions.push({ id: "diagnostics", primary: false });
      }

      return {
        tone: "danger",
        title: input.rawErrorName || t("timeline.apiError"),
        message,
        monoMessage: hasRaw,
        showStatusCode: true,
        showHealthBadge: true,
        actions,
      };
    }
  }
}
