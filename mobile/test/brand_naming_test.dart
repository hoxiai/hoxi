import 'dart:convert';
import 'dart:io';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:hoxi/core/brand/brand.dart';
import 'package:hoxi/l10n/gen/app_localizations.dart';

Map<String, dynamic> _readArb(String path) =>
    json.decode(File(path).readAsStringSync()) as Map<String, dynamic>;

Set<String> _messageKeys(Map<String, dynamic> arb) =>
    arb.keys.where((key) => !key.startsWith('@')).toSet();

void main() {
  group('brand naming', () {
    test('Chinese name and English name are both Hoxi', () {
      expect(Brand.nameZh, 'Hoxi');
      expect(Brand.nameEn, 'Hoxi');
      expect(Brand.localizedName(const Locale('zh')), 'Hoxi');
      expect(Brand.localizedName(const Locale('zh', 'CN')), 'Hoxi');
      expect(Brand.localizedName(const Locale('en')), 'Hoxi');
      expect(Brand.localizedName(const Locale('ja')), 'Hoxi');
    });

    test('localized suffix keeps the brand name per locale', () {
      expect(Brand.withSuffix(const Locale('zh'), 'Mac'), 'Hoxi · Mac');
      expect(Brand.withSuffix(const Locale('en'), 'Mac'), 'Hoxi · Mac');
      expect(Brand.withSuffix(const Locale('en'), '  '), 'Hoxi');
    });

    test('appName and voiceCallSelf are localized in both ARB files', () {
      final en = _readArb('lib/l10n/arb/app_en.arb');
      final zh = _readArb('lib/l10n/arb/app_zh.arb');
      expect(en['appName'], 'Hoxi');
      expect(zh['appName'], 'Hoxi');
      expect(en['voiceCallSelf'], 'Xiaole');
      expect(zh['voiceCallSelf'], '小乐');
      expect(Brand.voiceAssistantNameZh, '小乐');
      expect(Brand.voiceAssistantNameEn, 'Xiaole');
      expect(Brand.localizedVoiceAssistantName(const Locale('zh')), '小乐');
      expect(Brand.localizedVoiceAssistantName(const Locale('en')), 'Xiaole');
    });
  });

  group('localization resources', () {
    test('every English message has a Chinese translation', () {
      final en = _readArb('lib/l10n/arb/app_en.arb');
      final zh = _readArb('lib/l10n/arb/app_zh.arb');
      final missing = _messageKeys(en).difference(_messageKeys(zh));
      final extra = _messageKeys(zh).difference(_messageKeys(en));
      expect(missing, isEmpty, reason: 'Missing zh keys: $missing');
      expect(extra, isEmpty, reason: 'Keys only in zh: $extra');
    });

    test('generated localizations expose the expected locales', () {
      expect(
        AppLocalizations.supportedLocales.map((locale) => locale.languageCode),
        containsAll(<String>['en', 'zh']),
      );
    });
  });

  group('no third-party product names in source', () {
    test('lib/ and test/ do not mention third-party voice product names', () {
      // Build the banned tokens at runtime so this file itself is not a hit.
      final banned = <String>['\u7075\u5149', 'Ling' 'guang'];
      final offenders = <String>[];
      for (final folder in <String>['lib', 'test']) {
        for (final entity in Directory(folder).listSync(recursive: true)) {
          if (entity is! File) continue;
          if (!entity.path.endsWith('.dart')) continue;
          final text = entity.readAsStringSync();
          if (banned.any(text.contains)) offenders.add(entity.path);
        }
      }
      expect(
        offenders,
        isEmpty,
        reason: 'Replace third-party product names with Brand / appName: '
            '${offenders.join(', ')}',
      );
    });
  });
}
