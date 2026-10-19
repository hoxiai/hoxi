import 'package:flutter_test/flutter_test.dart';
import 'package:gopanel/features/ai_workspace/models/code_conversation_stream.dart';
import 'package:gopanel/features/auth/data/auth_repository.dart';
import 'package:gopanel/shared/models/server_connection.dart';

void main() {
  group('ServerConnection nodeType compatibility', () {
    test('defaults to gopanel when nodeType is omitted in legacy JSON', () {
      final conn = ServerConnection.fromJson({
        'id': 'node_1',
        'name': 'Prod Panel',
        'url': 'https://panel.example.com',
        'entrance': 'admin',
        'token': 'jwt_token',
        'lastConnectedAt': '2026-10-19T00:00:00.000',
      });
      expect(conn.nodeType, 'gopanel');
      expect(conn.isGoPanel, isTrue);
      expect(conn.isHoxi, isFalse);
    });

    test('persists and restores hoxi nodeType', () {
      final conn = ServerConnection(
        id: 'hoxi_1',
        name: 'Hoxi Mac Studio',
        url: 'http://192.168.1.50:4096',
        entrance: '',
        token: '',
        nodeType: 'hoxi',
        lastConnectedAt: DateTime(2026, 10, 19),
      );
      final restored = ServerConnection.fromJson(conn.toJson());
      expect(restored.nodeType, 'hoxi');
      expect(restored.isHoxi, isTrue);
      expect(restored.isGoPanel, isFalse);
    });
  });

  group('Hoxi SSE frame parsing', () {
    test('maps Hoxi message.part.delta and session.status to stream events', () {
      final parser = CodeConversationStreamParser();
      final events = parser.add(
        'data: {"type":"message.part.delta","properties":{"field":"text","delta":"Hello Hoxi"}}\n\n'
        'data: {"type":"message.part.updated","properties":{"part":{"type":"tool","tool":"bash","state":{"status":"running","title":"Run tests"}}}}\n\n'
        'data: {"type":"permission.asked","properties":{"id":"perm_1","title":"Allow rm -rf?"}}\n\n'
        'data: {"type":"session.status","properties":{"status":{"type":"idle"}}}\n\n',
      );
      expect(events, hasLength(4));
      expect(events[0].type, 'delta');
      expect(events[0].content, 'Hello Hoxi');
      expect(events[1].type, 'activity');
      expect(events[1].activity, '正在执行 bash');
      expect(events[2].type, 'status');
      expect(events[2].status, 'awaiting_approval');
      expect(events[3].type, 'done');
      expect(events[3].status, 'completed');
    });
  });

  group('Hoxi auth header probe builder', () {
    test('builds Basic auth when Basic token is provided', () {
      final opts = buildHoxiProbeOptions(
        entrance: '',
        token: 'Basic b3BlbmNvZGU6c2VjcmV0',
      );
      expect(opts.headers?['Authorization'], 'Basic b3BlbmNvZGU6c2VjcmV0');
      expect(opts.extra?['allowRawJson'], isTrue);
    });
  });
}
