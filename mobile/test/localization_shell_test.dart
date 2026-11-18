import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:hoxi/core/brand/brand.dart';
import 'package:hoxi/core/l10n/l10n.dart';

Widget _subject(String locale) {
  return MaterialApp(
    locale: Locale(locale),
    localizationsDelegates: AppLocalizations.localizationsDelegates,
    supportedLocales: AppLocalizations.supportedLocales,
    home: Builder(
      builder: (context) => Scaffold(
        body: Column(
          children: [
            Text(context.l10n.appName),
            Text(context.l10n.voiceCallSelf),
            Text(context.l10n.navChat),
            Text(context.l10n.voiceCallTitle),
            Text(context.l10n.settingsOverviewTitle),
            Text(context.l10n.loginConnect),
            Text(
              context.l10n.chatPendingApprovals(2),
            ),
            Text(context.l10n.chatComposerSkillHint('研发与审批')),
          ],
        ),
      ),
    ),
  );
}

void main() {
  testWidgets('Chinese locale renders Hoxi and Chinese labels', (
    tester,
  ) async {
    await tester.pumpWidget(_subject('zh'));
    await tester.pumpAndSettle();

    expect(find.text('Hoxi'), findsOneWidget);
    expect(find.text('小乐'), findsOneWidget);
    expect(find.text('沟通'), findsOneWidget);
    expect(find.text('语音通话'), findsOneWidget);
    expect(find.text('我的'), findsOneWidget);
    expect(find.text('连 接'), findsOneWidget);
    expect(find.text('2 项待审批'), findsOneWidget);
    expect(find.text('向 @研发与审批 发消息或按住说话…'), findsOneWidget);
  });

  testWidgets('English locale renders Hoxi and English labels', (
    tester,
  ) async {
    await tester.pumpWidget(_subject('en'));
    await tester.pumpAndSettle();

    expect(find.text('Hoxi'), findsOneWidget);
    expect(find.text('Xiaole'), findsOneWidget);
    expect(find.text('Chat'), findsOneWidget);
    expect(find.text('Voice call'), findsOneWidget);
    expect(find.text('Me'), findsOneWidget);
    expect(find.text('Connect'), findsOneWidget);
    expect(find.text('2 pending approval(s)'), findsOneWidget);
    expect(
      find.text('Message @研发与审批 or hold to talk…'),
      findsOneWidget,
    );
    expect(find.text('可喜'), findsNothing);
  });

  testWidgets('brand initials follow the locale', (tester) async {
    await tester.pumpWidget(
      MaterialApp(
        locale: const Locale('zh'),
        localizationsDelegates: AppLocalizations.localizationsDelegates,
        supportedLocales: AppLocalizations.supportedLocales,
        home: Builder(
          builder: (context) => Text(
            Brand.localizedInitial(Localizations.localeOf(context)),
          ),
        ),
      ),
    );
    expect(find.text('可'), findsOneWidget);
  });
}
