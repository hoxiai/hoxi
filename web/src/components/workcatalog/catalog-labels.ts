import { t } from "../../i18n";

export function catalogDraftStatusLabel(status: string): string {
  switch (status) {
    case "draft":
      return t("workCatalog.draft");
    case "conflict":
      return t("workCatalog.conflict");
    case "publishing":
      return t("workCatalog.publishing");
    case "published":
      return t("workCatalog.published");
    case "unverified":
      return t("workCatalog.unverified");
    case "discarded":
      return t("workCatalog.discarded");
    default:
      return status || t("workCatalog.unknown");
  }
}

export function catalogPlatformStatusLabel(status: string): string {
  switch (status) {
    case "published":
      return t("workCatalog.publishedState");
    case "expired":
      return t("workCatalog.expiredState");
    case "new":
      return t("workCatalog.newState");
    case "modified":
      return t("workCatalog.modifiedState");
    case "deleted":
      return t("workCatalog.deletedState");
    default:
      return status || t("workCatalog.unknown");
  }
}
