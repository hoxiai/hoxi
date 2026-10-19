import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../../core/network/api_client.dart';
import '../../../../core/storage/storage_service.dart';
import '../../data/settings_repository.dart';
import '../../models/settings_info.dart';

final settingsRepositoryProvider = Provider<SettingsRepository>((ref) {
  return SettingsRepository(ApiClient());
});

class SettingsState {
  final bool isLoading;
  final bool isActionLoading;
  final String? errorMessage;
  final SettingsInfo? info;
  final SettingsVersion? version;
  final SettingsVersion? update;
  final UpgradeRecovery? recovery;

  const SettingsState({
    this.isLoading = false,
    this.isActionLoading = false,
    this.errorMessage,
    this.info,
    this.version,
    this.update,
    this.recovery,
  });

  SettingsState copyWith({
    bool? isLoading,
    bool? isActionLoading,
    String? errorMessage,
    SettingsInfo? info,
    SettingsVersion? version,
    SettingsVersion? update,
    UpgradeRecovery? recovery,
    bool clearRecovery = false,
  }) {
    return SettingsState(
      isLoading: isLoading ?? this.isLoading,
      isActionLoading: isActionLoading ?? this.isActionLoading,
      errorMessage: errorMessage,
      info: info ?? this.info,
      version: version ?? this.version,
      update: update ?? this.update,
      recovery: clearRecovery ? null : recovery ?? this.recovery,
    );
  }
}

final settingsControllerProvider =
    NotifierProvider<SettingsController, SettingsState>(SettingsController.new);

class SettingsController extends Notifier<SettingsState> {
  late SettingsRepository _repository;

  @override
  SettingsState build() {
    _repository = ref.watch(settingsRepositoryProvider);
    Future.microtask(loadInfo);
    Future.microtask(loadRecovery);
    return const SettingsState();
  }

  Future<void> loadRecovery() async {
    final data = StorageService.getUpgradeRecovery();
    if (data == null) {
      state = state.copyWith(clearRecovery: true);
      return;
    }
    final recovery = UpgradeRecovery.fromJson(data);
    final expired =
        recovery.startedAt != null &&
        DateTime.now().difference(recovery.startedAt!).inHours >= 24;
    state = expired
        ? state.copyWith(clearRecovery: true)
        : state.copyWith(recovery: recovery);
    if (expired) await StorageService.clearUpgradeRecovery();
  }

  Future<void> loadInfo() async {
    state = state.copyWith(isLoading: true, errorMessage: null);
    try {
      final data = await _repository.getInfo();
      state = state.copyWith(
        isLoading: false,
        info: SettingsInfo.fromJson(data),
      );
    } catch (error) {
      state = state.copyWith(isLoading: false, errorMessage: error.toString());
    }
  }

  Future<void> updatePassword({
    required String password,
    required String newPassword,
  }) async {
    await _runAction(
      () => _repository.updatePassword(
        password: password,
        newPassword: newPassword,
      ),
    );
  }

  Future<void> updatePort(int port) async {
    await _runAction(() => _repository.updatePort(port));
  }

  Future<void> updateEntrance(String entrance) async {
    await _runAction(() => _repository.updateEntrance(entrance));
  }

  Future<SettingsVersion?> loadVersion() async {
    state = state.copyWith(isLoading: true, errorMessage: null);
    try {
      final data = await _repository.getVersion();
      state = state.copyWith(
        isLoading: false,
        version: SettingsVersion.fromJson(data),
      );
      return state.version;
    } catch (error) {
      state = state.copyWith(isLoading: false, errorMessage: error.toString());
      return null;
    }
  }

  Future<SettingsVersion?> checkUpdate() async {
    state = state.copyWith(isActionLoading: true, errorMessage: null);
    try {
      final data = await _repository.checkUpdate();
      state = state.copyWith(
        isActionLoading: false,
        update: SettingsVersion.fromJson(data),
      );
      return state.update;
    } catch (error) {
      state = state.copyWith(
        isActionLoading: false,
        errorMessage: error.toString(),
      );
      return null;
    }
  }

  Future<String> startUpgrade({
    required String currentVersion,
    required String targetVersion,
  }) async {
    state = state.copyWith(isActionLoading: true, errorMessage: null);
    try {
      final log = await _repository.startUpgrade(
        currentVersion: currentVersion,
        targetVersion: targetVersion,
      );
      await StorageService.saveUpgradeRecovery(
        logName: log,
        currentVersion: currentVersion,
        targetVersion: targetVersion,
      );
      state = state.copyWith(isActionLoading: false);
      return log;
    } catch (error) {
      state = state.copyWith(
        isActionLoading: false,
        errorMessage: error.toString(),
      );
      rethrow;
    }
  }

  Future<List<String>> getUpgradeLogs(String logName) {
    return _repository.getUpgradeLogs(logName);
  }

  Future<UpgradeStatus?> getUpgradeStatus(String logName) async {
    try {
      final data = await _repository.getUpgradeStatus(logName);
      return UpgradeStatus.fromJson(data);
    } catch (_) {
      return null;
    }
  }

  Future<void> clearRecovery() async {
    await StorageService.clearUpgradeRecovery();
    state = state.copyWith(clearRecovery: true);
  }

  Future<void> restart(String operation) async {
    await _runAction(() => _repository.restart(operation));
  }

  Future<Map<String, dynamic>> clear(String key) async {
    state = state.copyWith(isActionLoading: true, errorMessage: null);
    try {
      final result = await _repository.clear(key);
      state = state.copyWith(isActionLoading: false);
      return result;
    } catch (error) {
      state = state.copyWith(
        isActionLoading: false,
        errorMessage: error.toString(),
      );
      rethrow;
    }
  }

  Future<void> _runAction(Future<void> Function() action) async {
    state = state.copyWith(isActionLoading: true, errorMessage: null);
    try {
      await action();
      state = state.copyWith(isActionLoading: false);
    } catch (error) {
      state = state.copyWith(
        isActionLoading: false,
        errorMessage: error.toString(),
      );
      rethrow;
    }
  }
}
