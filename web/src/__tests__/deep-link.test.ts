import { buildMessageLink } from "../components/timeline/session-export";
import { assertEqual, describe, it } from "./test-harness";

describe("session deep links", () => {
  it("builds an app-scheme link instead of a webview origin", () => {
    assertEqual(buildMessageLink("ses_abc123"), "hoxi://session/ses_abc123");
  });

  it("anchors a specific message", () => {
    assertEqual(
      buildMessageLink("ses_abc123", "msg_a_1792552422878_12"),
      "hoxi://session/ses_abc123?message=msg_a_1792552422878_12"
    );
  });

  it("escapes ids", () => {
    assertEqual(buildMessageLink("ses_a b", "msg/a"), "hoxi://session/ses_a%20b?message=msg%2Fa");
  });
});
