import 'package:flutter/widgets.dart';

/// 交付预览卡片文案：Code 交付之后衔接交付中心，生成预览、验收、发布生产与切回旧版本。
abstract final class CodeFlowText {
  static const _values = <String, Map<String, String>>{
    'zh': {
      'title': '交付预览',
      'startHint': '用这次交付的提交构建版本，并部署到预览环境供验收。',
      'autoStartHint': '已开启交付后自动生成预览。',
      'autoStartPending': '交付已完成，正在自动生成预览…',
      'start': '生成预览',
      'started': '已开始生成预览',
      'openPreview': '打开预览',
      'version': 'v{version}',
      'status.queued': '排队中',
      'status.running': '执行中',
      'status.success': '已发布',
      'status.failed': '失败',
      'status.waiting_deployment': '待发布',
      'stage.building': '正在构建',
      'stage.publishing': '正在生成 Release',
      'stage.deploying': '正在部署',
      'environment.preview': '预览环境',
      'environment.production': '生产环境',
      'state.pending': '待部署',
      'state.deploying': '部署中',
      'state.failed': '部署失败',
      'state.current': '当前在线',
      'state.replaced': '已被替换',
      'action.deploy': '部署',
      'action.release': '发布生产',
      'action.redeploy': '重新部署',
      'action.switchBack': '切回此版本',
      'confirmTitle': '部署到{environment}？',
      'confirmPreview':
          '把 v{version} 部署到{environment}（{website}）。切换前会做健康检查，失败会保留旧版本。',
      'confirmProduction':
          '把 v{version} 发布到生产环境（{website}），这会切换线上流量。切换前会做健康检查，稳定期内出现故障会自动恢复旧版本。',
      'confirm': '确认部署',
      'cancel': '取消',
      'deployQueued': '部署已进入执行队列',
      'blocked.runner_target_stale': '这个版本的运行环境已被更新的构建替换，需要在电脑端重新构建后才能部署。',
      'blocked.runner_target_unavailable':
          '这个版本的 Runner 容器已被清理，需要在电脑端重新构建后才能部署。',
      'blocked.runner_fixed_port': '流水线使用固定宿主机端口，预览和生产无法并存，请在电脑端改为自动分配端口。',
      'openFailed': '无法打开预览地址',
    },
    'en': {
      'title': 'Delivery preview',
      'startHint':
          "Build this delivery's commits and deploy them to the preview environment for acceptance.",
      'autoStartHint': 'Previews start automatically after delivery.',
      'autoStartPending':
          'Delivery finished. Generating the preview automatically…',
      'start': 'Generate preview',
      'started': 'Preview started',
      'openPreview': 'Open preview',
      'version': 'v{version}',
      'status.queued': 'Queued',
      'status.running': 'Running',
      'status.success': 'Released',
      'status.failed': 'Failed',
      'status.waiting_deployment': 'Awaiting release',
      'stage.building': 'Building',
      'stage.publishing': 'Publishing release',
      'stage.deploying': 'Deploying',
      'environment.preview': 'Preview',
      'environment.production': 'Production',
      'state.pending': 'Pending',
      'state.deploying': 'Deploying',
      'state.failed': 'Failed',
      'state.current': 'Live',
      'state.replaced': 'Replaced',
      'action.deploy': 'Deploy',
      'action.release': 'Release',
      'action.redeploy': 'Redeploy',
      'action.switchBack': 'Switch back',
      'confirmTitle': 'Deploy to {environment}?',
      'confirmPreview':
          'Deploy v{version} to {environment} ({website}). A health check runs before switching, and the previous version is kept if it fails.',
      'confirmProduction':
          'Release v{version} to production ({website}). This switches live traffic. A health check runs before switching, and the previous version is restored automatically if it fails during stabilization.',
      'confirm': 'Deploy',
      'cancel': 'Cancel',
      'deployQueued': 'Deployment queued',
      'blocked.runner_target_stale':
          "A newer build has replaced this version's runtime. Rebuild it on desktop before deploying.",
      'blocked.runner_target_unavailable':
          "This version's Runner container was cleaned up. Rebuild it on desktop before deploying.",
      'blocked.runner_fixed_port':
          'The pipeline uses a fixed host port, so preview and production cannot coexist. Switch to automatic ports on desktop.',
      'openFailed': 'Unable to open the preview URL',
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
