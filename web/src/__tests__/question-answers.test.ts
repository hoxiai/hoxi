// web/src/__tests__/question-answers.test.ts
import {
  clearQuestionRecords,
  findPendingQuestionForPart,
  findQuestionReply,
  questionSummaryText,
  rememberQuestionReject,
  rememberQuestionReply,
  rememberQuestionRequest,
  resolveQuestionItems,
} from "../store/question-answers";
import { setWorkspace } from "../store/workspace-state";
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

  it("matches running question tool parts to pendingQuestions by callID or sessionID", () => {
    try {
      const req = {
        id: "que_live_1",
        sessionID: "ses_q",
        questions: questions as any,
        tool: { messageID: "msg_q", callID: "call_live" },
      };
      setWorkspace("pendingQuestions", [req]);
      const runningPart = questionPart(
        { status: "running", input: { questions }, time: { start: 1 } },
        "call_live"
      );
      assertEqual(findPendingQuestionForPart(runningPart)?.id, "que_live_1");

      const completedPart = questionPart(
        {
          status: "completed",
          input: { questions },
          output: "",
          title: "Questions",
          metadata: {},
          time: { start: 1, end: 2 },
        },
        "call_live"
      );
      assertEqual(findPendingQuestionForPart(completedPart), undefined);
    } finally {
      setWorkspace("pendingQuestions", []);
    }
  });

  it("normalizes Antigravity ask_question string options and is_multi_select", () => {
    clearQuestionRecords();
    const agyQuestions = [
      {
        question: "How should we proceed?",
        options: ["(Recommended) Full implementation", "Read-only audit"],
        is_multi_select: true,
      },
    ];
    const part = questionPart({
      status: "completed",
      input: { questions: agyQuestions },
      output: "",
      title: "ask_question",
      metadata: { answers: [["(Recommended) Full implementation"]] },
      time: { start: 1, end: 2 },
    });
    const items = resolveQuestionItems(part);
    assertEqual(items.length, 1);
    assertEqual(items[0].multiple, true);
    assertEqual(items[0].options.length, 2);
    assertEqual(items[0].options[0].label, "(Recommended) Full implementation");
    assertEqual(items[0].options[0].selected, true);
    assertEqual(items[0].options[1].label, "Read-only audit");
    assertEqual(items[0].options[1].selected, false);
  });
});
