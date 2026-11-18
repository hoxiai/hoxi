import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:hoxi/features/auth/data/auth_repository.dart';

void main() {
  test(
    'connection options isolate and encode security entrance credentials',
    () {
      final options = buildConnectionOptions(
        entrance: 'private-entry',
        token: 'jwt-token',
      );

      expect(options.extra?['skipActiveCredentials'], isTrue);
      expect(options.headers?['X-Auth'], 'jwt-token');
      expect(
        options.headers?['EntranceCode'],
        base64Encode(utf8.encode('private-entry')),
      );
    },
  );

  test('connection options omit empty credentials', () {
    final options = buildConnectionOptions(entrance: '  ');

    expect(options.headers, isEmpty);
  });
}
