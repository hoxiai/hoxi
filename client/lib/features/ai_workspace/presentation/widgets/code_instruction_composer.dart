import 'package:flutter/material.dart';

import '../../models/code_instruction_options.dart';
import '../code_workspace_text.dart';

class CodeInstructionComposer extends StatelessWidget {
  const CodeInstructionComposer({
    super.key,
    required this.controller,
    required this.focusNode,
    required this.enabled,
    required this.closed,
    required this.options,
    required this.onOptionsChanged,
    required this.onSend,
  });

  final TextEditingController controller;
  final FocusNode focusNode;
  final bool enabled;
  final bool closed;
  final CodeInstructionOptions options;
  final ValueChanged<CodeInstructionOptions> onOptionsChanged;
  final VoidCallback onSend;

  void _applyMacro(String prompt) {
    if (!enabled) return;
    final current = controller.text.trim();
    if (current.isEmpty) {
      controller.text = prompt;
    } else {
      controller.text = '$current\n$prompt';
    }
    controller.selection = TextSelection.collapsed(
      offset: controller.text.length,
    );
    focusNode.requestFocus();
  }

  @override
  Widget build(BuildContext context) {
    final macros = [
      (
        CodeWorkspaceText.t(context, 'macro.test'),
        CodeWorkspaceText.t(context, 'macro.testPrompt'),
      ),
      (
        CodeWorkspaceText.t(context, 'macro.diagnose'),
        CodeWorkspaceText.t(context, 'macro.diagnosePrompt'),
      ),
      (
        CodeWorkspaceText.t(context, 'macro.preview'),
        CodeWorkspaceText.t(context, 'macro.previewPrompt'),
      ),
      (
        CodeWorkspaceText.t(context, 'macro.commit'),
        CodeWorkspaceText.t(context, 'macro.commitPrompt'),
      ),
      (
        CodeWorkspaceText.t(context, 'macro.quality'),
        CodeWorkspaceText.t(context, 'macro.qualityPrompt'),
      ),
    ];

    return Container(
      color: const Color(0xFF1E293B),
      padding: EdgeInsets.fromLTRB(
        12,
        8,
        8,
        MediaQuery.paddingOf(context).bottom + 10,
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          SingleChildScrollView(
            scrollDirection: Axis.horizontal,
            child: Row(
              children: [
                _OptionChip(
                  icon: Icons.preview_outlined,
                  label: CodeWorkspaceText.t(
                    context,
                    'instruction.autoPreview',
                  ),
                  selected: options.autoPreview,
                  enabled: enabled,
                  onSelected: (selected) =>
                      onOptionsChanged(options.copyWith(autoPreview: selected)),
                ),
                const SizedBox(width: 8),
                _OptionChip(
                  icon: Icons.gpp_maybe_outlined,
                  label: CodeWorkspaceText.t(
                    context,
                    'instruction.requireApproval',
                  ),
                  selected: options.requireApproval,
                  enabled: enabled,
                  onSelected: (selected) => onOptionsChanged(
                    options.copyWith(requireApproval: selected),
                  ),
                ),
                const SizedBox(width: 8),
                Container(
                  height: 18,
                  width: 1,
                  color: Colors.white24,
                  margin: const EdgeInsets.symmetric(horizontal: 4),
                ),
                for (final macro in macros) ...[
                  const SizedBox(width: 8),
                  _MacroChip(
                    label: macro.$1,
                    enabled: enabled,
                    onTap: () => _applyMacro(macro.$2),
                  ),
                ],
              ],
            ),
          ),
          const SizedBox(height: 4),
          Row(
            crossAxisAlignment: CrossAxisAlignment.end,
            children: [
              const Padding(
                padding: EdgeInsets.only(bottom: 12, right: 8),
                child: Text(
                  '\$',
                  style: TextStyle(
                    color: Colors.greenAccent,
                    fontSize: 18,
                    fontWeight: FontWeight.bold,
                  ),
                ),
              ),
              Expanded(
                child: TextField(
                  controller: controller,
                  focusNode: focusNode,
                  enabled: enabled,
                  maxLines: 5,
                  minLines: 1,
                  textInputAction: TextInputAction.send,
                  onSubmitted: (_) => onSend(),
                  style: const TextStyle(color: Colors.white),
                  decoration: InputDecoration(
                    hintText: CodeWorkspaceText.t(
                      context,
                      closed
                          ? 'instruction.closedHint'
                          : enabled
                          ? 'instruction.inputHint'
                          : 'instruction.selectSessionHint',
                    ),
                    hintStyle: const TextStyle(color: Colors.white38),
                    border: InputBorder.none,
                  ),
                ),
              ),
              IconButton(
                tooltip: CodeWorkspaceText.t(context, 'instruction.send'),
                onPressed: enabled ? onSend : null,
                icon: const Icon(Icons.send_rounded),
                color: Colors.blueAccent,
              ),
            ],
          ),
        ],
      ),
    );
  }
}

class _MacroChip extends StatelessWidget {
  const _MacroChip({
    required this.label,
    required this.enabled,
    required this.onTap,
  });

  final String label;
  final bool enabled;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return ActionChip(
      label: Text(label),
      onPressed: enabled ? onTap : null,
      visualDensity: VisualDensity.compact,
      backgroundColor: const Color(0xFF0F172A),
      disabledColor: const Color(0xFF1E293B),
      side: const BorderSide(color: Color(0xFF334155)),
      labelStyle: TextStyle(
        color: enabled ? const Color(0xFF93C5FD) : Colors.white24,
        fontSize: 12,
        fontWeight: FontWeight.w500,
      ),
    );
  }
}

class _OptionChip extends StatelessWidget {
  const _OptionChip({
    required this.icon,
    required this.label,
    required this.selected,
    required this.enabled,
    required this.onSelected,
  });

  final IconData icon;
  final String label;
  final bool selected;
  final bool enabled;
  final ValueChanged<bool> onSelected;

  @override
  Widget build(BuildContext context) {
    return FilterChip(
      avatar: Icon(icon, size: 16),
      label: Text(label),
      selected: selected,
      onSelected: enabled ? onSelected : null,
      showCheckmark: false,
      visualDensity: VisualDensity.compact,
      backgroundColor: const Color(0xFF334155),
      selectedColor: const Color(0xFF1D4ED8),
      disabledColor: const Color(0xFF334155),
      side: BorderSide(
        color: selected ? const Color(0xFF60A5FA) : const Color(0xFF475569),
      ),
      labelStyle: TextStyle(
        color: enabled ? Colors.white : Colors.white38,
        fontSize: 12,
      ),
      iconTheme: IconThemeData(
        color: enabled ? Colors.white70 : Colors.white30,
      ),
    );
  }
}
