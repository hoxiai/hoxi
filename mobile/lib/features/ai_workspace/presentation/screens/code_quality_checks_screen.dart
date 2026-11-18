import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../../core/theme/app_theme.dart';
import '../../data/ai_workspace_repository.dart';
import '../../models/code_quality_check.dart';
import '../controllers/ai_workspace_controller.dart';
import '../../../../core/l10n/l10n.dart';

class CodeQualityChecksScreen extends ConsumerStatefulWidget {
  const CodeQualityChecksScreen({
    super.key,
    required this.sessionId,
    required this.sessionTitle,
  });

  final int sessionId;
  final String sessionTitle;

  @override
  ConsumerState<CodeQualityChecksScreen> createState() =>
      _CodeQualityChecksScreenState();
}

class _CodeQualityChecksScreenState
    extends ConsumerState<CodeQualityChecksScreen> {
  late final AiWorkspaceRepository _repo;
  bool _isLoading = false;
  String? _errorMessage;
  List<CodeQualityCheck> _checks = const [];
  final Set<String> _runningCheckIds = {};
  final Set<String> _expandedCheckIds = {};

  @override
  void initState() {
    super.initState();
    _repo = ref.read(aiWorkspaceRepositoryProvider);
    _loadChecks();
  }

  Future<void> _loadChecks() async {
    setState(() {
      _isLoading = true;
      _errorMessage = null;
    });
    try {
      final items = await _repo.getQualityChecks(widget.sessionId);
      if (!mounted) return;
      setState(() {
        _checks = items;
        _isLoading = false;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _errorMessage = e.toString();
        _isLoading = false;
      });
    }
  }

  Future<void> _runSingleCheck(CodeQualityCheck check) async {
    if (_runningCheckIds.contains(check.id)) return;
    setState(() => _runningCheckIds.add(check.id));
    try {
      final result = await _repo.runQualityCheck(widget.sessionId, check.id);
      if (!mounted) return;
      setState(() {
        _checks = _checks.map((c) {
          return c.id == check.id ? c.copyWith(lastResult: result) : c;
        }).toList();
        _expandedCheckIds.add(check.id);
      });
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text(
            '${check.label}: ${result.isPassed ? context.l10n.codeWsQualityPassed : context.l10n.codeWsQualityFailed}',
          ),
          backgroundColor:
              result.isPassed ? const Color(0xFF10B981) : AppTheme.error,
        ),
      );
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text(
            '${context.l10n.codeWsQualityRunFailed}: $e',
          ),
          backgroundColor: AppTheme.error,
        ),
      );
    } finally {
      if (mounted) {
        setState(() => _runningCheckIds.remove(check.id));
      }
    }
  }

  Future<void> _runAllChecks() async {
    if (_checks.isEmpty || _runningCheckIds.isNotEmpty) return;
    for (final check in _checks) {
      if (!mounted) break;
      await _runSingleCheck(check);
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(context.l10n.codeWsQualityTitle),
            Text(
              widget.sessionTitle,
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
              style: const TextStyle(fontSize: 11),
            ),
          ],
        ),
        actions: [
          if (_checks.isNotEmpty)
            TextButton.icon(
              onPressed: _runningCheckIds.isNotEmpty ? null : _runAllChecks,
              icon: _runningCheckIds.isNotEmpty
                  ? const SizedBox.square(
                      dimension: 14,
                      child: CircularProgressIndicator(strokeWidth: 2),
                    )
                  : const Icon(Icons.play_arrow_rounded, size: 18),
              label: Text(context.l10n.codeWsQualityRunAll),
            ),
          IconButton(
            onPressed: _isLoading ? null : _loadChecks,
            icon: const Icon(Icons.refresh_rounded),
          ),
        ],
      ),
      body: RefreshIndicator(
        onRefresh: _loadChecks,
        child: _buildBody(),
      ),
    );
  }

  Widget _buildBody() {
    if (_isLoading && _checks.isEmpty) {
      return ListView(
        physics: const AlwaysScrollableScrollPhysics(),
        children: const [
          SizedBox(height: 180),
          Center(child: CircularProgressIndicator()),
        ],
      );
    }

    if (_errorMessage != null && _checks.isEmpty) {
      return ListView(
        physics: const AlwaysScrollableScrollPhysics(),
        padding: const EdgeInsets.all(24),
        children: [
          const SizedBox(height: 72),
          const Icon(Icons.error_outline_rounded, size: 48, color: AppTheme.error),
          const SizedBox(height: 12),
          Text(
            context.l10n.codeWsQualityLoadFailed,
            textAlign: TextAlign.center,
            style: const TextStyle(fontWeight: FontWeight.bold),
          ),
          const SizedBox(height: 8),
          Text(_errorMessage!, textAlign: TextAlign.center),
          const SizedBox(height: 16),
          Center(
            child: OutlinedButton(
              onPressed: _loadChecks,
              child: Text(context.l10n.codeWsActionRetry),
            ),
          ),
        ],
      );
    }

    if (_checks.isEmpty) {
      return ListView(
        physics: const AlwaysScrollableScrollPhysics(),
        padding: const EdgeInsets.all(24),
        children: [
          const SizedBox(height: 80),
          const Icon(Icons.verified_outlined, size: 48, color: Colors.blueAccent),
          const SizedBox(height: 12),
          Text(
            context.l10n.codeWsQualityEmpty,
            textAlign: TextAlign.center,
            style: const TextStyle(fontWeight: FontWeight.w600, fontSize: 16),
          ),
          const SizedBox(height: 8),
          Text(
            context.l10n.codeWsQualityEmptyHint,
            textAlign: TextAlign.center,
            style:  TextStyle(color: AppTheme.textSecondary, fontSize: 13),
          ),
        ],
      );
    }

    return ListView.separated(
      physics: const AlwaysScrollableScrollPhysics(),
      padding: const EdgeInsets.all(16),
      itemCount: _checks.length,
      separatorBuilder: (context, index) => const SizedBox(height: 12),
      itemBuilder: (context, index) {
        final check = _checks[index];
        final isRunning = _runningCheckIds.contains(check.id);
        final isExpanded = _expandedCheckIds.contains(check.id);
        return _QualityCheckCard(
          check: check,
          isRunning: isRunning,
          isExpanded: isExpanded,
          onToggleExpand: () {
            setState(() {
              if (isExpanded) {
                _expandedCheckIds.remove(check.id);
              } else {
                _expandedCheckIds.add(check.id);
              }
            });
          },
          onRun: () => _runSingleCheck(check),
        );
      },
    );
  }
}

class _QualityCheckCard extends StatelessWidget {
  const _QualityCheckCard({
    required this.check,
    required this.isRunning,
    required this.isExpanded,
    required this.onToggleExpand,
    required this.onRun,
  });

  final CodeQualityCheck check;
  final bool isRunning;
  final bool isExpanded;
  final VoidCallback onToggleExpand;
  final VoidCallback onRun;

  @override
  Widget build(BuildContext context) {
    final result = check.lastResult;
    final hasResult = result != null;

    Color statusColor = AppTheme.textSecondary;
    String statusLabel = context.l10n.codeWsQualityPending;
    IconData statusIcon = Icons.hourglass_empty_rounded;

    if (isRunning) {
      statusColor = AppTheme.primary;
      statusLabel = context.l10n.codeWsQualityRunning;
      statusIcon = Icons.sync_rounded;
    } else if (hasResult) {
      if (result.isPassed) {
        statusColor = AppTheme.success;
        statusLabel = context.l10n.codeWsQualityPassed;
        statusIcon = Icons.check_circle_rounded;
      } else {
        statusColor = AppTheme.error;
        statusLabel = context.l10n.codeWsQualityFailed;
        statusIcon = Icons.cancel_rounded;
      }
    }

    return Container(
      decoration: BoxDecoration(
        color: AppTheme.surface,
        borderRadius: BorderRadius.circular(16),
        border: Border.all(
          color: isRunning
              ? AppTheme.primary.withValues(alpha: 0.5)
              : hasResult && !result.isPassed
              ? AppTheme.error.withValues(alpha: 0.3)
              : AppTheme.border,
        ),
        boxShadow: [
          BoxShadow(
            color: Colors.black.withValues(alpha: AppTheme.isDark ? 0.22 : 0.04),
            blurRadius: 10,
            offset: const Offset(0, 4),
          ),
        ],
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Padding(
            padding: const EdgeInsets.all(16),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Row(
                  children: [
                    Container(
                      padding: const EdgeInsets.symmetric(
                        horizontal: 8,
                        vertical: 3,
                      ),
                      decoration: BoxDecoration(
                        color: AppTheme.surfaceMuted,
                        borderRadius: BorderRadius.circular(6),
                      ),
                      child: Text(
                        check.kind.toUpperCase(),
                        style: TextStyle(
                          fontSize: 10,
                          fontWeight: FontWeight.w700,
                          color: AppTheme.textSecondary,
                        ),
                      ),
                    ),
                    const SizedBox(width: 8),
                    Expanded(
                      child: Text(
                        check.label,
                        style:  TextStyle(
                          fontSize: 15,
                          fontWeight: FontWeight.bold,
                          color: AppTheme.textPrimary,
                        ),
                      ),
                    ),
                    Container(
                      padding: const EdgeInsets.symmetric(
                        horizontal: 8,
                        vertical: 4,
                      ),
                      decoration: BoxDecoration(
                        color: statusColor.withValues(alpha: 0.1),
                        borderRadius: BorderRadius.circular(999),
                      ),
                      child: Row(
                        mainAxisSize: MainAxisSize.min,
                        children: [
                          Icon(statusIcon, size: 13, color: statusColor),
                          const SizedBox(width: 4),
                          Text(
                            statusLabel,
                            style: TextStyle(
                              color: statusColor,
                              fontSize: 11,
                              fontWeight: FontWeight.w600,
                            ),
                          ),
                        ],
                      ),
                    ),
                  ],
                ),
                const SizedBox(height: 10),
                Container(
                  width: double.infinity,
                  padding: const EdgeInsets.symmetric(
                    horizontal: 10,
                    vertical: 6,
                  ),
                  decoration: BoxDecoration(
                    color: const Color(0xFF0F172A),
                    borderRadius: BorderRadius.circular(8),
                  ),
                  child: SelectableText(
                    '\$ ${check.command}',
                    style: const TextStyle(
                      color: Color(0xFF38BDF8),
                      fontFamily: 'monospace',
                      fontSize: 11,
                    ),
                  ),
                ),
                if (hasResult) ...[
                  const SizedBox(height: 8),
                  Row(
                    children: [
                      Text(
                        context.l10n.codeWsQualityDuration(result.durationMs),
                        style:  TextStyle(
                          fontSize: 11,
                          color: AppTheme.textSecondary,
                        ),
                      ),
                      const SizedBox(width: 12),
                      Text(
                        context.l10n.codeWsQualityExitCode(result.exitCode),
                        style: TextStyle(
                          fontSize: 11,
                          fontWeight: FontWeight.w600,
                          color: result.exitCode == 0
                              ? const Color(0xFF10B981)
                              : AppTheme.error,
                        ),
                      ),
                    ],
                  ),
                ],
                const SizedBox(height: 12),
                Row(
                  children: [
                    if (hasResult && result.output.isNotEmpty)
                      OutlinedButton.icon(
                        onPressed: onToggleExpand,
                        icon: Icon(
                          isExpanded
                              ? Icons.keyboard_arrow_up_rounded
                              : Icons.keyboard_arrow_down_rounded,
                          size: 16,
                        ),
                        label: Text(
                          isExpanded ? '收起日志' : '查看日志',
                          style: const TextStyle(fontSize: 12),
                        ),
                        style: OutlinedButton.styleFrom(
                          padding: const EdgeInsets.symmetric(horizontal: 12),
                          visualDensity: VisualDensity.compact,
                        ),
                      ),
                    const Spacer(),
                    FilledButton.icon(
                      onPressed: isRunning ? null : onRun,
                      icon: isRunning
                          ? const SizedBox.square(
                              dimension: 12,
                              child: CircularProgressIndicator(
                                strokeWidth: 2,
                                color: Colors.white,
                              ),
                            )
                          : const Icon(Icons.play_arrow_rounded, size: 16),
                      label: Text(
                        context.l10n.codeWsQualityRun,
                        style: const TextStyle(fontSize: 12),
                      ),
                      style: FilledButton.styleFrom(
                        padding: const EdgeInsets.symmetric(horizontal: 16),
                        visualDensity: VisualDensity.compact,
                      ),
                    ),
                  ],
                ),
              ],
            ),
          ),
          if (isExpanded && hasResult && result.output.isNotEmpty)
            Container(
              width: double.infinity,
              padding: const EdgeInsets.all(12),
              decoration: const BoxDecoration(
                color: Color(0xFF0F172A),
                borderRadius: BorderRadius.vertical(
                  bottom: Radius.circular(15),
                ),
              ),
              child: SingleChildScrollView(
                scrollDirection: Axis.horizontal,
                child: SelectableText(
                  result.output,
                  style: const TextStyle(
                    color: Color(0xFFCBD5E1),
                    fontFamily: 'monospace',
                    fontSize: 11,
                    height: 1.4,
                  ),
                ),
              ),
            ),
        ],
      ),
    );
  }
}
