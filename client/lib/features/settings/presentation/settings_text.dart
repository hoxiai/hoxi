import 'package:flutter/widgets.dart';

abstract final class SettingsText {
  static const _values = <String, Map<String, String>>{
    'zh': {
      'upgradeTitle': '升级进度',
      'upgradeRunning': '正在执行升级，请保持网络连接',
      'upgradePreparing': '正在准备升级',
      'upgradeDownloading': '正在下载升级包',
      'upgradeVerifying': '正在校验升级包',
      'upgradeInstalling': '正在安装新版本',
      'upgradeRestarting': '正在重启面板服务',
      'upgradeFinished': '升级任务已结束，正在校验版本',
      'upgradeVerified': '升级完成，版本已校验',
      'waitingLogs': '等待升级日志…',
      'upgradeFailed': '升级失败，请查看日志后重试',
      'verifyTimeout': '升级任务已结束，但暂未确认新版本',
      'verifyAgain': '重新校验',
      'retryUpgrade': '重试升级',
      'retryConfirmTitle': '确认重试升级',
      'retryConfirmContent': '将再次尝试升级到 {version}，现在开始吗？',
      'cancel': '取消',
      'confirmRetry': '确认重试',
      'clearTask': '清除任务',
      'retryFailed': '升级任务重试失败',
      'versionUnavailable': '暂时无法确认当前版本，请稍后重试',
      'clearFailed': '清除升级任务失败',
    },
    'en': {
      'upgradeTitle': 'Upgrade progress',
      'upgradeRunning': 'Upgrade in progress. Keep the network connected.',
      'upgradePreparing': 'Preparing the upgrade',
      'upgradeDownloading': 'Downloading the upgrade package',
      'upgradeVerifying': 'Verifying the upgrade package',
      'upgradeInstalling': 'Installing the new version',
      'upgradeRestarting': 'Restarting the panel service',
      'upgradeFinished': 'Upgrade finished. Verifying the version…',
      'upgradeVerified': 'Upgrade completed and the version is verified.',
      'waitingLogs': 'Waiting for upgrade logs…',
      'upgradeFailed': 'Upgrade failed. Review the log and try again.',
      'verifyTimeout':
          'Upgrade finished, but the new version is not confirmed.',
      'verifyAgain': 'Verify again',
      'retryUpgrade': 'Retry upgrade',
      'retryConfirmTitle': 'Retry upgrade?',
      'retryConfirmContent': 'Try upgrading to {version} again now?',
      'cancel': 'Cancel',
      'confirmRetry': 'Retry',
      'clearTask': 'Clear task',
      'retryFailed': 'Failed to retry the upgrade',
      'versionUnavailable':
          'The current version is unavailable. Please try again later.',
      'clearFailed': 'Failed to clear the upgrade task',
    },
  };

  static String t(BuildContext context, String key) {
    final language = View.of(context).platformDispatcher.locale.languageCode;
    return _values[language]?[key] ?? _values['en']![key] ?? key;
  }

  static String format(
    BuildContext context,
    String key,
    Map<String, Object> values,
  ) {
    var text = t(context, key);
    for (final entry in values.entries) {
      text = text.replaceAll('{${entry.key}}', entry.value.toString());
    }
    return text;
  }
}
