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
}
