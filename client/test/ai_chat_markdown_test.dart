import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:hoxi/features/ai_workspace/models/chat_message.dart';
import 'package:hoxi/features/ai_workspace/presentation/widgets/ai_chat_markdown.dart';
import 'package:hoxi/features/ai_workspace/presentation/widgets/ai_chat_message_item.dart';

Future<void> _pumpMessage(WidgetTester tester, ChatMessage message) {
  return tester.pumpWidget(
    MaterialApp(
      theme: ThemeData.dark(),
      home: Scaffold(
        body: SingleChildScrollView(child: AiChatMessageItem(message: message)),
      ),
    ),
  );
}

// 可选中的正文由 SelectableText 渲染，其余（如图片说明）仍是 RichText
bool _anyTextContains(WidgetTester tester, String needle) {
  final selectable = tester
      .widgetList<SelectableText>(find.byType(SelectableText))
      .map((widget) => widget.textSpan?.toPlainText() ?? widget.data ?? '');
  final rich = tester
      .widgetList<RichText>(find.byType(RichText))
      .map((widget) => widget.text.toPlainText());
  return [...selectable, ...rich].any((text) => text.contains(needle));
}

void main() {
  testWidgets('AI 回复按 markdown 渲染、可选中，不加载图片', (tester) async {
    await _pumpMessage(
      tester,
      ChatMessage(
        id: 'stream-1',
        text:
            '## 修改结果\n'
            '已修复 **结算金额**：\n\n'
            '- 改了 `cart.go`\n'
            '- 补了测试\n\n'
            '```go\nfunc Total() int { return 1 }\n```\n\n'
            '![截图](https://tracker.example.com/pixel.png)\n'
            '[预览](https://preview.example.com)',
        isUser: false,
        timestamp: DateTime(2026),
      ),
    );

    expect(_anyTextContains(tester, '修改结果'), isTrue);
    expect(_anyTextContains(tester, '## 修改结果'), isFalse);
    expect(_anyTextContains(tester, '**'), isFalse);
    expect(_anyTextContains(tester, 'func Total() int'), isTrue);
    expect(find.byType(SelectableText), findsWidgets);
    expect(find.byType(Image), findsNothing);
    expect(find.text('截图'), findsOneWidget);
  });

  testWidgets('单个换行保留为换行，命令输出不会挤成一行', (tester) async {
    await _pumpMessage(
      tester,
      ChatMessage(
        id: 'stream-2',
        text: 'ok  app/api\nok  app/service',
        isUser: false,
        timestamp: DateTime(2026),
      ),
    );
    expect(_anyTextContains(tester, 'ok  app/api\nok  app/service'), isTrue);
  });

  test('只放行 http/https 链接', () {
    expect(
      aiChatLinkUri('https://preview.example.com')?.host,
      'preview.example.com',
    );
    expect(aiChatLinkUri('http://10.0.0.2:8080'), isNotNull);
    expect(aiChatLinkUri('file:///etc/passwd'), isNull);
    expect(aiChatLinkUri('intent://scan#Intent;end'), isNull);
    expect(aiChatLinkUri(null), isNull);
  });
}
