// web/src/__tests__/question-answers.test.ts
import {
  clearQuestionRecords,
  findQuestionReply,
  questionSummaryText,
  rememberQuestionReject,
  rememberQuestionReply,
  rememberQuestionRequest,
  resolveQuestionItems,
} from "../store/question-answers";
import type { ToolPart } from "../services/sse-types";
import { assertDeepEqual, assertEqual, assertTrue, describe, it } from "./test-harness";

const questions = [
  {
    question: "Which platform?",
    header: "Platform",
    options: [
      { label: "macOS", description: "Apple platforms" },
      { label: "Linux", description: "Server" },
    ],
    multiple: false,
  },
  {
    question: "Which modules?",
    header: "Modules",
    options: [
      { label: "Auth", description: "" },
      { label: "Billing", description: "" },
    ],
    multiple: true,
  },
];

const questionPart = (state: ToolPart["state"], callID = "call_1"): ToolPart =>
  ({
    id: "prt_q",
    sessionID: "ses_q",
    messageID: "msg_q",
    type: "tool",
    callID,
    tool: "question",
    state,
  }) as ToolPart;

describe("question-answers resolveQuestionItems", () => {
  it("marks selected options and custom answers from metadata answers", () => {
    clearQuestionRecords();
    const part = questionPart({
      status: "completed",
      input: { questions },
      output: "",
      title: "Questions",
      metadata: { answers: [["macOS", "Custom pick"], ["Auth"]] },
      time: { start: 1, end: 2 },
    });
    const items = resolveQuestionItems(part);
    assertEqual(items.length, 2);
    assertEqual(items[0].options[0].selected, true);
    assertEqual(items[0].options[1].selected, false);
    assertDeepEqual(items[0].customAnswers, ["Custom pick"]);
    assertDeepEqual(items[1].answers, ["Auth"]);
    assertEqual(items[1].answered, true);
  });

  it("falls back to structured output JSON answers", () => {
    clearQuestionRecords();
    const part = questionPart({
      status: "completed",
      input: { questions },
      output: JSON.stringify({ answers: [["Linux"], []] }),
      title: "Questions",
      metadata: {},
      time: { start: 1, end: 2 },
    });
    const items = resolveQuestionItems(part);
    assertEqual(items[0].options[1].selected, true);
    assertEqual(items[1].answered, false);
  });

  it("prefers the recorded reply over tool state answers", () => {
    clearQuestionRecords();
    const part = questionPart({
      status: "completed",
      input: { questions },
      output: "",
      title: "Questions",
      metadata: { answers: [["macOS"], []] },
      time: { start: 1, end: 2 },
    });
    rememberQuestionRequest({
      id: "req_1",
      sessionID: "ses_q",
      questions: questions as any,
      tool: { messageID: "msg_q", callID: "call_1" },
    });
    rememberQuestionReply("req_1", "ses_q", [["Linux"], ["Billing"]]);
    const reply = findQuestionReply("call_1");
    assertTrue(Boolean(reply), "expected reply record linked by callID");
    const items = resolveQuestionItems(part, reply);
    assertEqual(items[0].options[1].selected, true);
    assertEqual(items[0].options[0].selected, false);
    assertEqual(items[1].options[1].selected, true);
  });

  it("parses model-facing answer text as a last resort", () => {
    clearQuestionRecords();
    const part = questionPart({
      status: "completed",
      input: { questions },
      output:
        'User has answered your questions: "Which platform?"="macOS", "Which modules?"="Unanswered".',
      title: "Questions",
      metadata: {},
      time: { start: 1, end: 2 },
    });
    const items = resolveQuestionItems(part);
    assertEqual(items[0].options[0].selected, true);
    assertEqual(items[1].answered, false);
  });

  it("tracks rejected questions and summarizes selections", () => {
    clearQuestionRecords();
    rememberQuestionRequest({
      id: "req_2",
      sessionID: "ses_q",
      questions: questions as any,
      tool: { messageID: "msg_q", callID: "call_2" },
    });
    rememberQuestionReject("req_2", "ses_q");
    assertEqual(findQuestionReply("call_2")?.rejected, true);

    rememberQuestionReply("req_3", "ses_q", [["macOS"], ["Auth", "Billing"]]);
    const part = questionPart({
      status: "completed",
      input: { questions },
      output: "",
      title: "Questions",
      metadata: { answers: [["macOS"], ["Auth", "Billing"]] },
      time: { start: 1, end: 2 },
    });
    const summary = questionSummaryText(part);
    assertTrue(summary.includes("macOS"), `summary should mention answer, got: ${summary}`);
    assertTrue(summary.includes("Billing"), `summary should mention answer, got: ${summary}`);
  });
});
