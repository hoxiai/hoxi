import 'package:flutter_test/flutter_test.dart';

import 'package:hoxi/features/ai_workspace/models/code_conversation_stream.dart';

void main() {
  test('parses split SSE frames and preserves event type', () {
    final parser = CodeConversationStreamParser();
    expect(
      parser.add('event: delta\ndata: {"runId":7,"content":"hel'),
      isEmpty,
    );
    final events = parser.add(
      'lo","status":"running"}\n\n'
      ': ping\n\n'
      'event: done\ndata: {"runId":7,"content":"hello","status":"completed"}\n\n',
    );
    expect(events, hasLength(2));
    expect(events[0].type, 'delta');
    expect(events[0].runId, 7);
    expect(events[0].content, 'hello');
    expect(events[1].type, 'done');
    expect(events[1].status, 'completed');
  });

  test('supports CRLF and multiline data fields', () {
    final parser = CodeConversationStreamParser();
    final events = parser.add(
      'event: activity\r\n'
      'data: {"type":"activity",\r\n'
      'data: "runId":3,"activity":"editing"}\r\n\r\n',
    );
    expect(events, hasLength(1));
    expect(events.single.type, 'activity');
    expect(events.single.runId, 3);
    expect(events.single.activity, 'editing');
  });

  test('routes Codex inline <thinking> and reasoning part deltas out of visible text', () {
    final parser = CodeConversationStreamParser();
    final events = parser.add(
      'data: {"type":"message.part.updated","properties":{"part":{"id":"prt_r1","type":"reasoning","text":""}}}\n\n'
      'data: {"type":"message.part.delta","properties":{"partID":"prt_r1","delta":"**Running build checks****Checking Flutter status**"}}\n\n'
      'data: {"type":"message.part.delta","properties":{"partID":"prt_t1","delta":"<thi"}}\n\n'
      'data: {"type":"message.part.delta","properties":{"partID":"prt_t1","delta":"nking>**Reviewing frontend issue**</th"}}\n\n'
      'data: {"type":"message.part.delta","properties":{"partID":"prt_t1","delta":"inking>\\n\\nAll checks passed."}}\n\n',
    );

    expect(events, hasLength(4));
    expect(events[0].type, 'activity');
    expect(events[0].activityKind, 'thinking');
    expect(events[1].type, 'activity');
    expect(events[1].activityKind, 'thinking');
    expect(
      events[1].content,
      '**Running build checks**\n\n**Checking Flutter status**',
    );
    expect(events[2].type, 'activity');
    expect(events[2].activityKind, 'thinking');
    expect(events[2].content, '**Reviewing frontend issue**');
    expect(events[3].type, 'delta');
    expect(events[3].content, 'All checks passed.');
  });

  test('stripInlineThinking strips closed and unclosed <thinking> blocks', () {
    expect(
      stripInlineThinking(
        '<thinking>**Running build and mobile checks****Checking Go build**</thinking>\n\nFinal answer.',
      ),
      'Final answer.',
    );
    expect(
      stripInlineThinking(
        '<thinking>**Running build and mobile checks****Checking Go build**',
      ),
      isEmpty,
    );
  });

  test('streams cumulative message.part.updated text and reasoning without dropping later updates', () {
    final parser = CodeConversationStreamParser();
    final events = parser.add(
      'data: {"type":"message.part.updated","properties":{"part":{"id":"prt_r2","type":"reasoning","text":"Step 1"}}}\n\n'
      'data: {"type":"message.part.updated","properties":{"part":{"id":"prt_r2","type":"reasoning","text":"Step 1, Step 2"}}}\n\n'
      'data: {"type":"message.part.updated","properties":{"part":{"id":"prt_t2","type":"text","text":"Hello"}}}\n\n'
      'data: {"type":"message.part.updated","properties":{"part":{"id":"prt_t2","type":"text","text":"Hello world"}}}\n\n'
      'data: {"type":"message.part.delta","properties":{"partID":"prt_t2","delta":"!"}}\n\n'
      'data: {"type":"message.part.updated","properties":{"part":{"id":"prt_t2","type":"text","text":"Hello world!"}}}\n\n',
    );

    expect(events, hasLength(5));
    expect(events[0].type, 'activity');
    expect(events[0].activityKind, 'thinking');
    expect(events[0].content, 'Step 1');
    expect(events[1].type, 'activity');
    expect(events[1].content, ', Step 2');
    expect(events[2].type, 'delta');
    expect(events[2].content, 'Hello');
    expect(events[3].type, 'delta');
    expect(events[3].content, ' world');
    expect(events[4].type, 'delta');
    expect(events[4].content, '!');
  });

  test('ignores user message parts and strips user echo if part arrived before message.updated', () {
    final parser = CodeConversationStreamParser();
    final events = parser.add(
      'data: {"type":"message.updated","properties":{"info":{"id":"msg_u1","role":"user"}}}\n\n'
      'data: {"type":"message.part.updated","properties":{"part":{"id":"prt_u1","messageID":"msg_u1","type":"text","text":"测试一下"}}}\n\n'
      'data: {"type":"message.part.updated","properties":{"part":{"id":"prt_u2","messageID":"msg_u2","type":"text","text":"先发分片"}}}\n\n'
      'data: {"type":"message.updated","properties":{"info":{"id":"msg_u2","role":"user"}}}\n\n'
      'data: {"type":"message.updated","properties":{"info":{"id":"msg_a1","role":"assistant"}}}\n\n'
      'data: {"type":"message.part.delta","properties":{"partID":"prt_a1","messageID":"msg_a1","delta":"测试成功。"}}\n\n',
    );

    expect(events, hasLength(3));
    expect(events[0].type, 'delta');
    expect(events[0].content, '先发分片');
    expect(events[1].type, 'strip_user_echo');
    expect(events[1].content, '先发分片');
    expect(events[2].type, 'delta');
    expect(events[2].content, '测试成功。');
  });

  test(
    'detects reasoning deltas before part.updated and separates multi-step text/reasoning parts with double newlines',
    () {
      final parser = CodeConversationStreamParser();
      final events = parser.add(
        'data: {"type":"message.part.delta","properties":{"partID":"msg_1_reason_0","delta":"First thought."}}\n\n'
        'data: {"type":"message.part.delta","properties":{"partID":"msg_1_text_0","delta":"Step 1 output."}}\n\n'
        'data: {"type":"message.part.updated","properties":{"part":{"id":"prt_tool_1","type":"tool","tool":"bash","state":{"status":"running","input":{"command":"flutter test"}}}}}\n\n'
        'data: {"type":"message.part.delta","properties":{"partID":"msg_2_r_0","field":"reasoning","delta":"Second thought."}}\n\n'
        'data: {"type":"message.part.delta","properties":{"partID":"msg_2_text_0","delta":"Step 2 output."}}\n\n'
        'data: {"type":"hoxi.stream.resync","properties":{}}\n\n',
      );

      expect(events, hasLength(6));
      expect(events[0].type, 'activity');
      expect(events[0].activityKind, 'thinking');
      expect(events[0].content, 'First thought.');
      expect(events[1].type, 'delta');
      expect(events[1].content, 'Step 1 output.');
      expect(events[2].type, 'activity');
      expect(events[2].activityKind, 'tool');
      expect(events[2].activity, '正在执行 bash');
      expect(events[2].content, 'bash · flutter test');
      expect(events[3].type, 'activity');
      expect(events[3].activityKind, 'thinking');
      expect(events[3].content, '\n\nSecond thought.');
      expect(events[4].type, 'delta');
      expect(events[4].content, '\n\nStep 2 output.');
      expect(events[5].type, 'resync');
    },
  );

  test(
    'formats todowrite and question tool steps and parses question.asked / question.replied SSE events',
    () {
      final parser = CodeConversationStreamParser();
      final events = parser.add(
        'data: {"type":"message.part.updated","properties":{"part":{"id":"prt_td","type":"tool","tool":"todowrite","state":{"status":"completed","input":{"todos":[{"id":"1","content":"Fix stream","status":"completed"},{"id":"2","content":"Add diff shelf","status":"in_progress"}]}}}}}\n\n'
        'data: {"type":"message.part.updated","properties":{"part":{"id":"prt_q","type":"tool","tool":"question","state":{"status":"running","input":{"questions":[{"question":"Which branch should we target?"}]}}}}}\n\n'
        'data: {"type":"question.asked","properties":{"questions":[{"question":"Which branch should we target?"}]}}\n\n'
        'data: {"type":"question.replied","properties":{}}\n\n',
      );

      expect(events, hasLength(4));
      expect(events[0].content, 'todowrite · 1/2 项任务');
      expect(events[1].content, 'question · Which branch should we target?');
      expect(events[2].activityKind, 'question_waiting');
      expect(events[2].activity, '等待回答：Which branch should we target?');
      expect(events[3].activityKind, 'question_resolved');
      expect(events[3].activity, '问题已回答，继续执行');
    },
  );

  test('emits session_updated event when session.updated frame contains a title', () {
    final parser = CodeConversationStreamParser(targetSessionId: 'ses_target');
    final events = parser.add(
      'data: {"type":"session.updated","properties":{"sessionID":"ses_other","info":{"id":"ses_other","title":"Other Title"}}}\n\n'
      'data: {"type":"session.updated","properties":{"info":{"id":"ses_target","title":"自动生成的会话标题"}}}\n\n',
    );
    expect(events, hasLength(1));
    expect(events.single.type, 'session_updated');
    expect(events.single.content, '自动生成的会话标题');
  });
}
