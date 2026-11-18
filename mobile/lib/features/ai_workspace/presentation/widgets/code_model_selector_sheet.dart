import 'package:flutter/material.dart';

import '../../../../core/l10n/l10n.dart';
import '../../../../core/theme/app_theme.dart';
import '../../../../core/theme/vendor_colors.dart';
import '../../models/ai_dev_session.dart';
import '../../models/code_model_catalog.dart';

part 'code_model_selector_widgets.dart';

class CodeModelSelectorField extends StatelessWidget {
  const CodeModelSelectorField({
    super.key,
    required this.executors,
    required this.selectedExecutor,
    required this.selectedModel,
    required this.onSelected,
    this.selectedEffort = '',
    this.executorIdHint = '',
    this.showReasoningEffortInSheet = true,
    this.allowCrossExecutorSelection = true,
  });

  final List<CodeExecutor> executors;
  final CodeExecutor? selectedExecutor;
  final String selectedModel;
  final String selectedEffort;
  final String executorIdHint;
  final bool showReasoningEffortInSheet;
  final bool allowCrossExecutorSelection;
  final ValueChanged<CodeModelSelection> onSelected;

  Future<void> _openPicker(BuildContext context) async {
    final result = await CodeModelSelectorSheet.show(
      context,
      executors: executors,
      selectedExecutor: selectedExecutor,
      selectedModel: selectedModel,
      selectedEffort: selectedEffort,
      executorIdHint: executorIdHint,
      showReasoningEffort: showReasoningEffortInSheet,
      allowCrossExecutorSelection: allowCrossExecutorSelection,
    );
    if (result != null) {
      onSelected(result);
    }
  }

  @override
  Widget build(BuildContext context) {
    final trimmedModel = selectedModel.trim();
    final matched =
        CodeModelCatalog.findSelectedModel(
          executors: executors,
          selectedExecutor: selectedExecutor,
          modelValue: trimmedModel,
          executorIdHint: executorIdHint,
        ) ??
        (trimmedModel.isEmpty
            ? CodeModelCatalog.defaultModelForExecutors(
                executors,
                preferredExecutor: selectedExecutor,
                executorIdHint: executorIdHint,
              )
            : null);
    final hasEffectiveModel = trimmedModel.isNotEmpty || matched != null;
    final displayTitle = !hasEffectiveModel
        ? context.l10n.codeWsSessionSettingsModelHint
        : (matched?.displayName ?? matched?.label ?? trimmedModel);
    final providerBadge =
        matched?.providerName ?? selectedExecutor?.name ?? '';
    final vendorColor = VendorColors.forVendor(
      providerBadge.isEmpty ? displayTitle : providerBadge,
    );

    return InkWell(
      key: const ValueKey('code_model_selector_field'),
      borderRadius: BorderRadius.circular(14),
      onTap: () => _openPicker(context),
      child: InputDecorator(
        decoration: InputDecoration(
          labelText: context.l10n.codeWsSessionSettingsModel,
          border: const OutlineInputBorder(),
          contentPadding: const EdgeInsets.symmetric(
            horizontal: 14,
            vertical: 12,
          ),
        ),
        child: Row(
          children: [
            Container(
              width: 24,
              height: 24,
              decoration: BoxDecoration(
                color: hasEffectiveModel
                    ? vendorColor.withValues(alpha: AppTheme.isDark ? 0.16 : 0.1)
                    : AppTheme.surfaceMuted,
                borderRadius: BorderRadius.circular(7),
                border: Border.all(
                  color: hasEffectiveModel
                      ? vendorColor.withValues(alpha: 0.24)
                      : AppTheme.border,
                  width: 0.8,
                ),
              ),
              alignment: Alignment.center,
              child: Container(
                width: 6.5,
                height: 6.5,
                decoration: BoxDecoration(
                  color: hasEffectiveModel
                      ? vendorColor
                      : AppTheme.textSecondary,
                  shape: BoxShape.circle,
                ),
              ),
            ),
            const SizedBox(width: 10),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                mainAxisSize: MainAxisSize.min,
                children: [
                  Text(
                    displayTitle,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: TextStyle(
                      color: hasEffectiveModel
                          ? AppTheme.textPrimary
                          : AppTheme.textSecondary,
                      fontSize: 14,
                      fontWeight: hasEffectiveModel
                          ? FontWeight.w600
                          : FontWeight.w500,
                      letterSpacing: -0.2,
                    ),
                  ),
                  if (trimmedModel.isNotEmpty &&
                      matched != null &&
                      matched.value != displayTitle) ...[
                    const SizedBox(height: 2),
                    Text(
                      matched.value,
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: TextStyle(
                        color: AppTheme.textLight,
                        fontSize: 11,
                        fontFamily: 'monospace',
                      ),
                    ),
                  ],
                ],
              ),
            ),
            if (providerBadge.isNotEmpty) ...[
              const SizedBox(width: 8),
              Container(
                padding: const EdgeInsets.symmetric(
                  horizontal: 8,
                  vertical: 3,
                ),
                decoration: BoxDecoration(
                  color: AppTheme.surfaceMuted,
                  borderRadius: BorderRadius.circular(6),
                  border: Border.all(
                    color: AppTheme.border.withValues(alpha: 0.75),
                    width: 0.7,
                  ),
                ),
                child: Text(
                  providerBadge,
                  style: TextStyle(
                    color: AppTheme.textSecondary,
                    fontSize: 11,
                    fontWeight: FontWeight.w600,
                  ),
                ),
              ),
            ],
            const SizedBox(width: 6),
            Icon(
              Icons.unfold_more_rounded,
              size: 17,
              color: AppTheme.textSecondary,
            ),
          ],
        ),
      ),
    );
  }
}

class CodeModelSelectorSheet extends StatefulWidget {
  const CodeModelSelectorSheet({
    super.key,
    required this.executors,
    required this.selectedExecutor,
    required this.selectedModel,
    this.selectedEffort = '',
    this.executorIdHint = '',
    this.showReasoningEffort = true,
    this.allowCrossExecutorSelection = true,
  });

  final List<CodeExecutor> executors;
  final CodeExecutor? selectedExecutor;
  final String selectedModel;
  final String selectedEffort;
  final String executorIdHint;
  final bool showReasoningEffort;
  final bool allowCrossExecutorSelection;

  static Future<CodeModelSelection?> show(
    BuildContext context, {
    required List<CodeExecutor> executors,
    required CodeExecutor? selectedExecutor,
    required String selectedModel,
    String selectedEffort = '',
    String executorIdHint = '',
    bool showReasoningEffort = true,
    bool allowCrossExecutorSelection = true,
  }) {
    return showModalBottomSheet<CodeModelSelection>(
      context: context,
      isScrollControlled: true,
      useSafeArea: true,
      backgroundColor: Colors.transparent,
      builder: (_) => CodeModelSelectorSheet(
        executors: executors,
        selectedExecutor: selectedExecutor,
        selectedModel: selectedModel,
        selectedEffort: selectedEffort,
        executorIdHint: executorIdHint,
        showReasoningEffort: showReasoningEffort,
        allowCrossExecutorSelection: allowCrossExecutorSelection,
      ),
    );
  }

  @override
  State<CodeModelSelectorSheet> createState() => _CodeModelSelectorSheetState();
}

class _CodeModelSelectorSheetState extends State<CodeModelSelectorSheet> {
  final _searchController = TextEditingController();
  final _customModelController = TextEditingController();
  String _selectedProviderId = 'all';
  late String _effort;
  bool _showCustomInput = false;

  @override
  void initState() {
    super.initState();
    _effort = widget.selectedEffort.trim();
    _customModelController.text = widget.selectedModel.trim();
  }

  @override
  void dispose() {
    _searchController.dispose();
    _customModelController.dispose();
    super.dispose();
  }

  void setEffortValue(String value) {
    setState(() => _effort = value);
  }

  void _selectModel(String modelValue, {String? executorId}) {
    Navigator.of(context).pop(
      CodeModelSelection(
        model: modelValue.trim(),
        executorId: executorId,
        reasoningEffort: _effort,
      ),
    );
  }

  static List<_CodeModelSection> _groupModels(List<CodeSelectableModel> models) {
    final sections = <_CodeModelSection>[];
    final indexByKey = <String, int>{};
    for (final item in models) {
      final key = item.providerId;
      final existingIdx = indexByKey[key];
      if (existingIdx == null) {
        indexByKey[key] = sections.length;
        sections.add(
          _CodeModelSection(
            providerId: item.providerId,
            providerName: item.providerName,
            executorName: item.executorName,
            items: [item],
          ),
        );
      } else {
        sections[existingIdx].items.add(item);
      }
    }
    return sections;
  }

  @override
  Widget build(BuildContext context) {
    final isZh = Localizations.localeOf(context).languageCode.startsWith('zh');
    final scopedExecutors = widget.allowCrossExecutorSelection
        ? widget.executors
        : <CodeExecutor>[?widget.selectedExecutor];
    final allModels = CodeModelCatalog.buildUnifiedCatalog(
      executors: scopedExecutors,
      selectedExecutor: widget.selectedExecutor,
      currentModel: widget.selectedModel,
      executorIdHint: widget.executorIdHint,
    );
    final groups = CodeModelCatalog.providerGroups(allModels);
    final query = _searchController.text.trim().toLowerCase();

    final filteredModels = allModels.where((item) {
      if (_selectedProviderId != 'all' &&
          item.providerId != _selectedProviderId) {
        return false;
      }
      if (query.isEmpty) return true;
      return item.label.toLowerCase().contains(query) ||
          item.displayName.toLowerCase().contains(query) ||
          item.value.toLowerCase().contains(query) ||
          item.providerName.toLowerCase().contains(query) ||
          item.executorName.toLowerCase().contains(query);
    }).toList();

    final effortOptions = CodeModelCatalog.effectiveReasoningEfforts(
      widget.selectedExecutor,
      currentEffort: _effort,
    );
    final currentModelTrimmed = widget.selectedModel.trim();
    final activeExecId =
        widget.selectedExecutor?.id ?? widget.executorIdHint.trim();
    final showDefaultItem = allModels.isEmpty && query.isEmpty;
    final sections = _groupModels(filteredModels);

    final matchedCurrent = CodeModelCatalog.findSelectedModel(
      executors: scopedExecutors,
      selectedExecutor: widget.selectedExecutor,
      modelValue: currentModelTrimmed,
      executorIdHint: widget.executorIdHint,
    );
    final currentDisplayLabel = matchedCurrent?.displayName ??
        (currentModelTrimmed.isNotEmpty
            ? currentModelTrimmed
            : context.l10n.codeWsSessionSettingsModelHint);
    final currentProviderColor = VendorColors.forVendor(
      matchedCurrent?.providerName ?? currentDisplayLabel,
    );

    return Container(
      constraints: BoxConstraints(
        maxHeight: MediaQuery.sizeOf(context).height * 0.84,
      ),
      decoration: BoxDecoration(
        color: AppTheme.background,
        borderRadius: const BorderRadius.vertical(top: Radius.circular(24)),
      ),
      child: SafeArea(
        top: false,
        child: Padding(
          padding: EdgeInsets.only(
            bottom: MediaQuery.viewInsetsOf(context).bottom,
          ),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              // Layer 1: 顶部控制层（白底/深色卡片顶栏，承载标题、当前锚点、搜索与厂商分段）
              Container(
                decoration: BoxDecoration(
                  color: AppTheme.surface,
                  borderRadius: const BorderRadius.vertical(
                    top: Radius.circular(24),
                  ),
                  border: Border(
                    bottom: BorderSide(
                      color: AppTheme.border.withValues(alpha: 0.65),
                    ),
                  ),
                ),
                padding: const EdgeInsets.fromLTRB(20, 10, 16, 14),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Center(
                      child: Container(
                        width: 36,
                        height: 4,
                        decoration: BoxDecoration(
                          color: AppTheme.border,
                          borderRadius: BorderRadius.circular(99),
                        ),
                      ),
                    ),
                    const SizedBox(height: 14),
                    Row(
                      crossAxisAlignment: CrossAxisAlignment.center,
                      children: [
                        Expanded(
                          child: Column(
                            crossAxisAlignment: CrossAxisAlignment.start,
                            children: [
                              Text(
                                context.l10n.chatModelPickerTitle,
                                style: TextStyle(
                                  fontSize: 17,
                                  fontWeight: FontWeight.w700,
                                  color: AppTheme.textPrimary,
                                  letterSpacing: -0.3,
                                ),
                              ),
                              const SizedBox(height: 3),
                              Row(
                                children: [
                                  Container(
                                    width: 6,
                                    height: 6,
                                    decoration: BoxDecoration(
                                      color: currentProviderColor,
                                      shape: BoxShape.circle,
                                    ),
                                  ),
                                  const SizedBox(width: 6),
                                  Flexible(
                                    child: Text(
                                      isZh
                                          ? '当前 · $currentDisplayLabel'
                                          : 'Active · $currentDisplayLabel',
                                      maxLines: 1,
                                      overflow: TextOverflow.ellipsis,
                                      style: TextStyle(
                                        fontSize: 12,
                                        fontWeight: FontWeight.w500,
                                        color: AppTheme.textSecondary,
                                      ),
                                    ),
                                  ),
                                ],
                              ),
                            ],
                          ),
                        ),
                        const SizedBox(width: 8),
                        InkWell(
                          key: const ValueKey('code_model_custom_toggle'),
                          borderRadius: BorderRadius.circular(999),
                          onTap: () {
                            setState(
                              () => _showCustomInput = !_showCustomInput,
                            );
                          },
                          child: Container(
                            padding: const EdgeInsets.symmetric(
                              horizontal: 11,
                              vertical: 6,
                            ),
                            decoration: BoxDecoration(
                              color: _showCustomInput
                                  ? AppTheme.primaryBlueLight
                                  : AppTheme.surfaceMuted,
                              borderRadius: BorderRadius.circular(999),
                              border: Border.all(
                                color: _showCustomInput
                                    ? AppTheme.primaryBlueBorder
                                    : AppTheme.border.withValues(alpha: 0.75),
                              ),
                            ),
                            child: Row(
                              mainAxisSize: MainAxisSize.min,
                              children: [
                                Icon(
                                  Icons.code_rounded,
                                  size: 13.5,
                                  color: _showCustomInput
                                      ? AppTheme.primaryBlueText
                                      : AppTheme.textSecondary,
                                ),
                                const SizedBox(width: 4),
                                Text(
                                  context.l10n.codeWsModelPickerCustomInput,
                                  style: TextStyle(
                                    fontSize: 12,
                                    fontWeight: FontWeight.w600,
                                    color: _showCustomInput
                                        ? AppTheme.primaryBlueText
                                        : AppTheme.textSecondary,
                                  ),
                                ),
                              ],
                            ),
                          ),
                        ),
                        const SizedBox(width: 6),
                        InkWell(
                          borderRadius: BorderRadius.circular(999),
                          onTap: () => Navigator.of(context).pop(),
                          child: Container(
                            width: 28,
                            height: 28,
                            decoration: BoxDecoration(
                              color: AppTheme.surfaceMuted,
                              shape: BoxShape.circle,
                            ),
                            child: Icon(
                              Icons.close_rounded,
                              size: 16,
                              color: AppTheme.textSecondary,
                            ),
                          ),
                        ),
                      ],
                    ),
                    if (_showCustomInput) ...[
                      const SizedBox(height: 12),
                      Row(
                        children: [
                          Expanded(
                            child: TextField(
                              key: const ValueKey('code_model_custom_field'),
                              controller: _customModelController,
                              style: TextStyle(
                                fontSize: 13,
                                fontFamily: 'monospace',
                                color: AppTheme.textPrimary,
                              ),
                              decoration: InputDecoration(
                                isDense: true,
                                filled: true,
                                fillColor: AppTheme.surfaceMuted,
                                hintText:
                                    context.l10n.codeWsModelPickerCustomHint,
                                hintStyle: TextStyle(
                                  fontSize: 12.5,
                                  fontFamily: 'monospace',
                                  color: AppTheme.textLight,
                                ),
                                border: OutlineInputBorder(
                                  borderRadius: BorderRadius.circular(11),
                                  borderSide: BorderSide(
                                    color: AppTheme.border.withValues(
                                      alpha: 0.7,
                                    ),
                                  ),
                                ),
                                enabledBorder: OutlineInputBorder(
                                  borderRadius: BorderRadius.circular(11),
                                  borderSide: BorderSide(
                                    color: AppTheme.border.withValues(
                                      alpha: 0.7,
                                    ),
                                  ),
                                ),
                                contentPadding: const EdgeInsets.symmetric(
                                  horizontal: 12,
                                  vertical: 10,
                                ),
                              ),
                              onSubmitted: (val) => _selectModel(
                                val,
                                executorId:
                                    CodeModelCatalog.resolveRuntimeForModel(
                                      model: val,
                                      explicitExecutorId:
                                          widget.selectedExecutor?.id,
                                      executors: widget.executors,
                                    ),
                              ),
                            ),
                          ),
                          const SizedBox(width: 8),
                          FilledButton(
                            key: const ValueKey('code_model_custom_confirm'),
                            style: FilledButton.styleFrom(
                              backgroundColor: AppTheme.primary,
                              foregroundColor: Colors.white,
                              padding: const EdgeInsets.symmetric(
                                horizontal: 14,
                                vertical: 10,
                              ),
                              minimumSize: const Size(0, 38),
                              shape: RoundedRectangleBorder(
                                borderRadius: BorderRadius.circular(11),
                              ),
                            ),
                            onPressed: () => _selectModel(
                              _customModelController.text,
                              executorId:
                                  CodeModelCatalog.resolveRuntimeForModel(
                                    model: _customModelController.text,
                                    explicitExecutorId:
                                        widget.selectedExecutor?.id,
                                    executors: widget.executors,
                                  ),
                            ),
                            child: Text(
                              context.l10n.codeWsAttentionConfirm,
                              style: const TextStyle(
                                fontSize: 12.5,
                                fontWeight: FontWeight.w600,
                              ),
                            ),
                          ),
                        ],
                      ),
                    ],
                    const SizedBox(height: 12),
                    TextField(
                      key: const ValueKey('code_model_search_field'),
                      controller: _searchController,
                      onChanged: (_) => setState(() {}),
                      style: TextStyle(
                        fontSize: 13.5,
                        color: AppTheme.textPrimary,
                      ),
                      decoration: InputDecoration(
                        isDense: true,
                        filled: true,
                        fillColor: AppTheme.surfaceMuted,
                        prefixIcon: Icon(
                          Icons.search_rounded,
                          size: 17,
                          color: AppTheme.textLight,
                        ),
                        prefixIconConstraints: const BoxConstraints(
                          minWidth: 36,
                          minHeight: 34,
                        ),
                        suffixIcon: _searchController.text.isEmpty
                            ? null
                            : IconButton(
                                visualDensity: VisualDensity.compact,
                                onPressed: () {
                                  _searchController.clear();
                                  setState(() {});
                                },
                                icon: Icon(
                                  Icons.clear_rounded,
                                  size: 15,
                                  color: AppTheme.textSecondary,
                                ),
                              ),
                        hintText: context.l10n.codeWsModelPickerSearchHint,
                        hintStyle: TextStyle(
                          fontSize: 13,
                          color: AppTheme.textLight,
                        ),
                        border: OutlineInputBorder(
                          borderRadius: BorderRadius.circular(12),
                          borderSide: BorderSide(
                            color: AppTheme.border.withValues(alpha: 0.65),
                          ),
                        ),
                        enabledBorder: OutlineInputBorder(
                          borderRadius: BorderRadius.circular(12),
                          borderSide: BorderSide(
                            color: AppTheme.border.withValues(alpha: 0.65),
                          ),
                        ),
                        focusedBorder: OutlineInputBorder(
                          borderRadius: BorderRadius.circular(12),
                          borderSide: BorderSide(
                            color: AppTheme.primary,
                            width: 1.2,
                          ),
                        ),
                        contentPadding: const EdgeInsets.symmetric(
                          horizontal: 12,
                          vertical: 9,
                        ),
                      ),
                    ),
                    if (groups.length > 1) ...[
                      const SizedBox(height: 10),
                      SingleChildScrollView(
                        scrollDirection: Axis.horizontal,
                        child: Row(
                          children: [
                            _ProviderFilterChip(
                              name: context.l10n.containerFilterAll,
                              count: allModels.length,
                              selected: _selectedProviderId == 'all',
                              onTap: () =>
                                  setState(() => _selectedProviderId = 'all'),
                            ),
                            for (final group in groups) ...[
                              const SizedBox(width: 7),
                              _ProviderFilterChip(
                                key: ValueKey('model_provider_${group.id}'),
                                name: group.name,
                                count: group.count,
                                selected: _selectedProviderId == group.id,
                                onTap: () => setState(
                                  () => _selectedProviderId = group.id,
                                ),
                              ),
                            ],
                          ],
                        ),
                      ),
                    ],
                  ],
                ),
              ),
              // Layer 0 + Layer 1: 分组卡片列表（背景底板衬托纯白/深色分组卡片）
              Flexible(
                child: filteredModels.isEmpty && !showDefaultItem
                    ? Padding(
                        padding: const EdgeInsets.symmetric(vertical: 36),
                        child: Center(
                          child: Text(
                            context.l10n.codeWsModelPickerEmpty,
                            style: TextStyle(
                              fontSize: 13,
                              color: AppTheme.textSecondary,
                            ),
                          ),
                        ),
                      )
                    : ListView(
                        shrinkWrap: true,
                        padding: const EdgeInsets.fromLTRB(16, 14, 16, 18),
                        children: [
                          if (showDefaultItem)
                            _buildDefaultOptionCard(
                              context,
                              isDefaultSelected: currentModelTrimmed.isEmpty,
                            ),
                          for (int sIdx = 0; sIdx < sections.length; sIdx++) ...[
                            if (sIdx > 0 || showDefaultItem)
                              const SizedBox(height: 14),
                            _buildSectionHeader(sections[sIdx]),
                            _buildSectionCard(
                              context,
                              section: sections[sIdx],
                              currentModelTrimmed: currentModelTrimmed,
                              activeExecId: activeExecId,
                            ),
                          ],
                        ],
                      ),
              ),
              // Layer 1: 底部推理强度分段栏（Segmented Control）
              if (widget.showReasoningEffort && effortOptions.isNotEmpty)
                _buildEffortBar(context, effortOptions),
            ],
          ),
        ),
      ),
    );
  }
}
