import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:gopanel/core/network/api_client.dart';
import 'package:gopanel/features/auth/data/auth_repository.dart';
import 'package:gopanel/features/auth/presentation/controllers/auth_controller.dart';

void main() {
  test('opening login does not validate a stale server connection', () async {
    final repository = _FakeAuthRepository();
    final container = ProviderContainer(
      overrides: [authRepositoryProvider.overrideWithValue(repository)],
    );
    addTearDown(container.dispose);

    final state = container.read(authControllerProvider);
    await Future<void>.delayed(Duration.zero);

    expect(state.isLoading, isFalse);
    expect(container.read(authControllerProvider).isLoading, isFalse);
    expect(repository.userInfoRequests, 0);
  });

  test('failed login always clears the loading state', () async {
    final repository = _FakeAuthRepository(loginError: Exception('denied'));
    final container = ProviderContainer(
      overrides: [authRepositoryProvider.overrideWithValue(repository)],
    );
    addTearDown(container.dispose);

    final future = container
        .read(authControllerProvider.notifier)
        .connectAndLogin(
          serverUrl: 'https://panel.example.com',
          entrance: 'private-entry',
          username: 'admin@example.com',
          password: 'secret',
        );

    expect(container.read(authControllerProvider).isLoading, isTrue);
    expect(await future, isFalse);
    expect(container.read(authControllerProvider).isLoading, isFalse);
    expect(
      container.read(authControllerProvider).errorMessage,
      contains('denied'),
    );
  });
}

class _FakeAuthRepository extends AuthRepository {
  _FakeAuthRepository({this.loginError}) : super(ApiClient());

  int userInfoRequests = 0;
  final Object? loginError;

  @override
  Future<String> login({
    required String serverUrl,
    required String entrance,
    required String name,
    required String password,
  }) async {
    if (loginError != null) throw loginError!;
    return 'token';
  }

  @override
  Future<Map<String, dynamic>> getUserInfo() async {
    userInfoRequests++;
    return const {};
  }
}
