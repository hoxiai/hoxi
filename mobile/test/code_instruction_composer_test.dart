import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:hoxi/core/l10n/l10n.dart';
import 'package:hoxi/features/ai_workspace/presentation/widgets/code_instruction_composer.dart';
import 'package:shared_preferences/shared_preferences.dart';

void main() {
  setUp(() {
    SharedPreferences.setMockInitialValues({});
  });

  testWidgets(
    'removes obsolete preview/approval chips and provides model selector and attachments',
    (tester) async {
      final controller = TextEditingController();
      final focusNode = FocusNode();
      addTearDown(controller.dispose);
      addTearDown(focusNode.dispose);
      var modelTapped = 0;
      var filePicked = 0;
      var galleryPicked = 0;
      var removedFile = '';

      Widget subject() => ProviderScope(
        child: MaterialApp(
          locale: const Locale('zh'),
          localizationsDelegates: AppLocalizations.localizationsDelegates,
          supportedLocales: AppLocalizations.supportedLocales,
          home: Scaffold(
            body: CodeInstructionComposer(
              controller: controller,
              focusNode: focusNode,
              enabled: true,
              closed: false,
              modelLabel: 'claude-sonnet-4-5',
              reasoningEffort: 'high',
              onSelectModel: () => modelTapped++,
              attachedFiles: const ['lib/main.dart'],
              onRemoveAttachedFile: (f) => removedFile = f,
              onPickWorkspaceFile: () => filePicked++,
              onPickFromGallery: () => galleryPicked++,
              onSend: () {},
            ),
          ),
        ),
      );

      await tester.pumpWidget(subject());

      // 不再渲染「自动收集预览」「危险操作先审批」等冗余胶囊
      expect(find.text('自动收集预览'), findsNothing);
      expect(find.text('危险操作先审批'), findsNothing);

      // 展示当前模型与推理强度，点击可切换；右侧按钮贴右对齐
      expect(find.text('claude-sonnet-4-5 · high'), findsOneWidget);
      final composerRight = tester
          .getRect(find.byType(CodeInstructionComposer))
          .right;
      final sendRight = tester
          .getRect(find.byKey(const ValueKey('code_composer_send_button')))
          .right;
      expect(composerRight - sendRight, lessThanOrEqualTo(24));
      await tester.tap(find.byKey(const ValueKey('overview_model_chip')));
      expect(modelTapped, 1);

      // 展示已挂载文件胶囊并支持移除
      expect(find.text('@lib/main.dart'), findsOneWidget);
      await tester.tap(find.byIcon(Icons.close_rounded));
      expect(removedFile, 'lib/main.dart');

      // 点击「附件」弹出选择菜单（引用项目文件 / 从相册选择图片）
      await tester.tap(
        find.byKey(const ValueKey('code_composer_attachment_button')),
      );
      await tester.pumpAndSettle();
      expect(find.text('引用工作区文件 (@文件)'), findsOneWidget);
      await tester.tap(find.byKey(const ValueKey('code_composer_attach_file')));
      await tester.pumpAndSettle();
      expect(filePicked, 1);

      await tester.tap(
        find.byKey(const ValueKey('code_composer_attachment_button')),
      );
      await tester.pumpAndSettle();
      await tester.tap(
        find.byKey(const ValueKey('code_composer_attach_gallery')),
      );
      await tester.pumpAndSettle();
      expect(galleryPicked, 1);
    },
  );

  testWidgets('disables model, attachment, and send actions with no active session', (
    tester,
  ) async {
    final controller = TextEditingController();
    final focusNode = FocusNode();
    addTearDown(controller.dispose);
    addTearDown(focusNode.dispose);
    var modelTapped = 0;
    var sends = 0;

    await tester.pumpWidget(
      ProviderScope(
        child: MaterialApp(
          localizationsDelegates: AppLocalizations.localizationsDelegates,
          supportedLocales: AppLocalizations.supportedLocales,
          home: Scaffold(
            body: CodeInstructionComposer(
              controller: controller,
              focusNode: focusNode,
              enabled: false,
              closed: false,
              onSelectModel: () => modelTapped++,
              onSend: () => sends++,
            ),
          ),
        ),
      ),
    );

    await tester.tap(find.byKey(const ValueKey('overview_model_chip')));
    await tester.tap(find.byKey(const ValueKey('code_composer_send_button')));
    expect(modelTapped, 0);
    expect(sends, 0);
  });
}
