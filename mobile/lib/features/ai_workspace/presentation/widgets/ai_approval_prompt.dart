import 'dart:convert';

import 'package:flutter/material.dart';

import '../../../../core/theme/app_theme.dart';
import '../../../../shared/widgets/stream_text_pacer.dart';
import '../../models/ai_dev_session.dart';

class AiApprovalPrompt extends StatefulWidget {
  const AiApprovalPrompt({
    super.key,
    required this.approval,
    required this.loading,
    required this.onDecision,
  });

  final AiApproval approval;
  final bool loading;
  final Future<void> Function(bool approved, String reason) onDecision;

  @override
  State<AiApprovalPrompt> createState() => _AiApprovalPromptState();
}

class _AiApprovalPromptState extends State<AiApprovalPrompt> {
  final _reasonController = TextEditingController();
  final Map<int, Set<String>> _selectedByQuestion = {};
  final Map<int, TextEditingController> _customControllers = {};

  @override
  void didUpdateWidget(covariant AiApprovalPrompt oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.approval.id != widget.approval.id) {
      _reasonController.clear();
      _selectedByQuestion.clear();
      for (final c in _customControllers.values) {
        c.clear();
      }
    }
  }

  @override
  void dispose() {
    _reasonController.dispose();
    for (final c in _customControllers.values) {
      c.dispose();
    }
    super.dispose();
  }

  TextEditingController _customControllerFor(int index) {
    return _customControllers.putIfAbsent(index, () {
      final controller = TextEditingController();
      controller.addListener(() {
        if (mounted) setState(() {});
      });
      return controller;
    });
  }

  void _toggleOption(int qIndex, String label, bool multiple) {
    setState(() {
      final set = _selectedByQuestion.putIfAbsent(qIndex, () => <String>{});
      if (multiple) {
        if (set.contains(label)) {
          set.remove(label);
        } else {
          set.add(label);
        }
      } else {
        if (set.length == 1 && set.contains(label)) {
          set.clear();
        } else {
          set
            ..clear()
            ..add(label);
        }
      }
    });
  }

  List<List<String>> _collectQuestionAnswers() {
    final questions = widget.approval.questions;
    return List<List<String>>.generate(questions.length, (idx) {
      final picked = <String>[
        ...?_selectedByQuestion[idx],
      ];
      final customText = (_customControllers[idx]?.text ?? '').trim();
      if (customText.isNotEmpty && !picked.contains(customText)) {
        picked.add(customText);
      }
      return picked;
    });
  }

  bool get _canSubmitQuestions {
    final questions = widget.approval.questions;
    if (questions.isEmpty) return false;
    final answers = _collectQuestionAnswers();
    return answers.every((a) => a.isNotEmpty);
  }

  Widget _buildQuestionPrompt(BuildContext context) {
    final questions = widget.approval.questions;
    final accent = AppTheme.primaryBlue;
    return Container(
      width: double.infinity,
      margin: const EdgeInsets.fromLTRB(16, 0, 16, 12),
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: AppTheme.surface,
        borderRadius: BorderRadius.circular(18),
        border: Border.all(color: accent.withValues(alpha: 0.45)),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Icon(Icons.help_outline_rounded, color: accent, size: 20),
              const SizedBox(width: 8),
              Expanded(
                child: Text(
                  widget.approval.title.isEmpty
                      ? '请选择或输入回答'
                      : widget.approval.title,
                  style: TextStyle(
                    color: AppTheme.textPrimary,
                    fontWeight: FontWeight.w700,
                  ),
                ),
              ),
            ],
          ),
          const SizedBox(height: 10),
          ConstrainedBox(
            constraints: const BoxConstraints(maxHeight: 280),
            child: SingleChildScrollView(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  for (var i = 0; i < questions.length; i++) ...[
                    if (i > 0) const SizedBox(height: 12),
                    _buildSingleQuestion(i, questions[i], accent),
                  ],
                ],
              ),
            ),
          ),
          const SizedBox(height: 12),
          Row(
            children: [
              Expanded(
                child: OutlinedButton(
                  onPressed: widget.loading
                      ? null
                      : () => widget.onDecision(false, ''),
                  child: const Text('跳过'),
                ),
              ),
              const SizedBox(width: 12),
              Expanded(
                child: FilledButton(
                  onPressed: (widget.loading || !_canSubmitQuestions)
                      ? null
                      : () => widget.onDecision(
                          true,
                          jsonEncode(_collectQuestionAnswers()),
                        ),
                  child: Text(widget.loading ? '提交中...' : '提交回答'),
                ),
              ),
            ],
          ),
        ],
      ),
    );
  }

  Widget _buildSingleQuestion(
    int index,
    AiQuestionItem item,
    Color accent,
  ) {
    final selected = _selectedByQuestion[index] ?? const <String>{};
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        if (item.header.isNotEmpty && item.header != item.question)
          Padding(
            padding: const EdgeInsets.only(bottom: 4),
            child: Text(
              item.header,
              style: TextStyle(
                color: accent,
                fontSize: 11.5,
                fontWeight: FontWeight.w600,
              ),
            ),
          ),
        if (item.question.isNotEmpty)
          Text(
            item.question,
            style: TextStyle(
              color: AppTheme.textPrimary,
              fontSize: 13.5,
              fontWeight: FontWeight.w600,
              height: 1.4,
            ),
          ),
        if (item.options.isNotEmpty) ...[
          const SizedBox(height: 8),
          for (final opt in item.options)
            Padding(
              padding: const EdgeInsets.only(bottom: 6),
              child: InkWell(
                borderRadius: BorderRadius.circular(10),
                onTap: widget.loading
                    ? null
                    : () => _toggleOption(index, opt.label, item.multiple),
                child: Container(
                  width: double.infinity,
                  padding: const EdgeInsets.symmetric(
                    horizontal: 11,
                    vertical: 9,
                  ),
                  decoration: BoxDecoration(
                    color: selected.contains(opt.label)
                        ? accent.withValues(alpha: 0.14)
                        : AppTheme.background,
                    borderRadius: BorderRadius.circular(10),
                    border: Border.all(
                      color: selected.contains(opt.label)
                          ? accent
                          : AppTheme.border,
                    ),
                  ),
                  child: Row(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Icon(
                        selected.contains(opt.label)
                            ? (item.multiple
                                  ? Icons.check_box_rounded
                                  : Icons.radio_button_checked_rounded)
                            : (item.multiple
                                  ? Icons.check_box_outline_blank_rounded
                                  : Icons.radio_button_unchecked_rounded),
                        size: 18,
                        color: selected.contains(opt.label)
                            ? accent
                            : AppTheme.textSecondary,
                      ),
                      const SizedBox(width: 8),
                      Expanded(
                        child: Column(
                          crossAxisAlignment: CrossAxisAlignment.start,
                          children: [
                            Text(
                              opt.label,
                              style: TextStyle(
                                color: AppTheme.textPrimary,
                                fontSize: 13,
                                fontWeight: selected.contains(opt.label)
                                    ? FontWeight.w600
                                    : FontWeight.w500,
                              ),
                            ),
                            if (opt.description.isNotEmpty) ...[
                              const SizedBox(height: 2),
                              Text(
                                opt.description,
                                style: TextStyle(
                                  color: AppTheme.textSecondary,
                                  fontSize: 11.5,
                                  height: 1.35,
                                ),
                              ),
                            ],
                          ],
                        ),
                      ),
                    ],
                  ),
                ),
              ),
            ),
        ],
        if (item.custom) ...[
          const SizedBox(height: 4),
          TextField(
            controller: _customControllerFor(index),
            contextMenuBuilder: buildConversationContextMenu,
            style: TextStyle(color: AppTheme.textPrimary, fontSize: 13),
            decoration: InputDecoration(
              isDense: true,
              hintText: item.options.isEmpty ? '请输入你的回答...' : '或输入自定义回答（可选）...',
              hintStyle: TextStyle(color: AppTheme.textSecondary, fontSize: 12),
            ),
          ),
        ],
      ],
    );
  }

  @override
  Widget build(BuildContext context) {
    if (widget.approval.isQuestion) {
      return _buildQuestionPrompt(context);
    }
    final isCritical = widget.approval.riskLevel == 'critical';
    final accent = isCritical ? AppTheme.error : AppTheme.warning;
    return Container(
      width: double.infinity,
      margin: const EdgeInsets.fromLTRB(16, 0, 16, 12),
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: isCritical ? AppTheme.errorSoft : AppTheme.warningSoft,
        borderRadius: BorderRadius.circular(18),
        border: Border.all(color: accent.withValues(alpha: 0.45)),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Icon(
                Icons.gpp_maybe_rounded,
                color: accent,
              ),
              const SizedBox(width: 8),
              Expanded(
                child: Text(
                  widget.approval.title.isEmpty
                      ? '高风险操作待确认'
                      : widget.approval.title,
                  style: TextStyle(
                    color: AppTheme.textPrimary,
                    fontWeight: FontWeight.w700,
                  ),
                ),
              ),
              if (isCritical)
                Container(
                  padding: const EdgeInsets.symmetric(
                    horizontal: 8,
                    vertical: 3,
                  ),
                  decoration: BoxDecoration(
                    color: AppTheme.error.withValues(alpha: 0.14),
                    borderRadius: BorderRadius.circular(6),
                  ),
                  child: const Text(
                    '高危拦截',
                    style: TextStyle(
                      color: AppTheme.error,
                      fontSize: 11,
                      fontWeight: FontWeight.w700,
                    ),
                  ),
                ),
            ],
          ),
          const SizedBox(height: 10),
          Container(
            width: double.infinity,
            constraints: const BoxConstraints(maxHeight: 160),
            padding: const EdgeInsets.all(10),
            decoration: BoxDecoration(
              color: AppTheme.surface,
              borderRadius: BorderRadius.circular(10),
            ),
            child: SingleChildScrollView(
              child: SelectableText(
                widget.approval.content,
                contextMenuBuilder: buildConversationContextMenu,
                style: TextStyle(
                  color: AppTheme.textPrimary,
                  fontFamily: 'monospace',
                  fontSize: 12.5,
                  height: 1.45,
                ),
              ),
            ),
          ),
          const SizedBox(height: 12),
          TextField(
            controller: _reasonController,
            contextMenuBuilder: buildConversationContextMenu,
            style: TextStyle(color: AppTheme.textPrimary),
            decoration: InputDecoration(
              labelText: '处理说明（可选）',
              labelStyle: TextStyle(color: AppTheme.textSecondary),
            ),
          ),
          const SizedBox(height: 12),
          Row(
            children: [
              Expanded(
                child: OutlinedButton(
                  onPressed: widget.loading
                      ? null
                      : () => widget.onDecision(false, _reasonController.text),
                  child: const Text('拒绝'),
                ),
              ),
              const SizedBox(width: 12),
              Expanded(
                child: FilledButton(
                  onPressed: widget.loading
                      ? null
                      : () => widget.onDecision(true, _reasonController.text),
                  child: Text(widget.loading ? '处理中...' : '仅允许一次'),
                ),
              ),
            ],
          ),
        ],
      ),
    );
  }
}
