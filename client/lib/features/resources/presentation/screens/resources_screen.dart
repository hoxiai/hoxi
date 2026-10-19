import 'package:flutter/material.dart';

import '../../../../core/theme/app_theme.dart';
import '../../../container/presentation/screens/container_list_screen.dart';
import '../../../apps/presentation/screens/apps_list_screen.dart';
import '../../../website/presentation/screens/website_list_screen.dart';
import '../../../database/presentation/screens/database_list_screen.dart';
import '../../../ssl/presentation/screens/ssl_list_screen.dart';
import '../../../../shared/widgets/panel/glass_tabs.dart';

enum ResourceTab { containers, apps, websites, databases, ssl }

class ResourcesScreen extends StatefulWidget {
  const ResourcesScreen({super.key});

  @override
  State<ResourcesScreen> createState() => _ResourcesScreenState();
}

class _ResourcesScreenState extends State<ResourcesScreen> {
  ResourceTab _tab = ResourceTab.containers;

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      body: SafeArea(
        bottom: false,
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            _header(),
            Padding(
              padding: const EdgeInsets.fromLTRB(16, 0, 16, 16),
              child: _segmented(),
            ),
            Expanded(child: _buildTabBody()),
          ],
        ),
      ),
    );
  }

  Widget _header() {
    return Padding(
      padding: const EdgeInsets.fromLTRB(20, 22, 20, 18),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.center,
        children: [
          Container(
            width: 48,
            height: 48,
            decoration: BoxDecoration(
              color: AppTheme.primaryBlueLight,
              borderRadius: BorderRadius.circular(16),
              border: Border.all(color: AppTheme.primaryBlueBorder),
            ),
            child: const Icon(
              Icons.widgets_rounded,
              color: AppTheme.primaryBlue,
              size: 25,
            ),
          ),
          const SizedBox(width: 14),
          const Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  '资源',
                  style: TextStyle(
                    color: AppTheme.textPrimary,
                    fontSize: 26,
                    height: 1.1,
                    fontWeight: FontWeight.w800,
                    letterSpacing: -0.5,
                  ),
                ),
                SizedBox(height: 6),
                Text(
                  '集中管理服务器上的各类资源',
                  style: TextStyle(
                    color: AppTheme.textSecondary,
                    fontSize: 13,
                    fontWeight: FontWeight.w500,
                  ),
                ),
              ],
            ),
          ),
          Container(
            padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 7),
            decoration: BoxDecoration(
              color: Colors.white.withValues(alpha: 0.72),
              borderRadius: BorderRadius.circular(999),
              border: Border.all(color: AppTheme.border),
            ),
            child: const Text(
              '5 类',
              style: TextStyle(
                color: AppTheme.textSecondary,
                fontSize: 12,
                fontWeight: FontWeight.w700,
              ),
            ),
          ),
        ],
      ),
    );
  }

  Widget _segmented() {
    return GlassTabs<ResourceTab>(
      items: const [
        GlassTabItem(
          value: ResourceTab.containers,
          label: '容器',
          icon: Icons.inventory_2_rounded,
        ),
        GlassTabItem(
          value: ResourceTab.websites,
          label: '网站',
          icon: Icons.language_rounded,
        ),
        GlassTabItem(
          value: ResourceTab.databases,
          label: '数据库',
          icon: Icons.storage_rounded,
        ),
        GlassTabItem(
          value: ResourceTab.ssl,
          label: 'SSL',
          icon: Icons.verified_user_rounded,
        ),
        GlassTabItem(
          value: ResourceTab.apps,
          label: '应用',
          icon: Icons.apps_rounded,
        ),
      ],
      selected: _tab,
      onChanged: (v) {
        setState(() => _tab = v);
      },
      outerPadding: const EdgeInsets.all(4),
      tabPadding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
      borderRadius: 20,
      tabRadius: 14,
      blurSigma: 16,
    );
  }

  Widget _buildTabBody() {
    switch (_tab) {
      case ResourceTab.containers:
        return const ContainerListScreen.embedded();
      case ResourceTab.apps:
        return const AppsListScreen.embedded();
      case ResourceTab.websites:
        return const WebsiteListScreen.embedded();
      case ResourceTab.databases:
        return const DatabaseListScreen.embedded();
      case ResourceTab.ssl:
        return const SslListScreen.embedded();
    }
  }
}
