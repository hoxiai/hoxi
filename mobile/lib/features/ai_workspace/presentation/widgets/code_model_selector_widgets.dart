part of 'code_model_selector_sheet.dart';

class _CodeModelSection {
  const _CodeModelSection({
    required this.providerId,
    required this.providerName,
    required this.executorName,
    required this.items,
  });

  final String providerId;
  final String providerName;
  final String executorName;
  final List<CodeSelectableModel> items;
}

extension _CodeModelSelectorSheetCards on _CodeModelSelectorSheetState {
  Widget _buildEffortBar(
    BuildContext context,
    List<CodeExecutorOption> effortOptions,
  ) {
    return Container(
      decoration: BoxDecoration(
        color: AppTheme.surface,
        border: Border(
          top: BorderSide(
            color: AppTheme.border.withValues(alpha: 0.65),
          ),
        ),
      ),
      padding: const EdgeInsets.fromLTRB(16, 10, 16, 12),
      child: Row(
        children: [
          Text(
            context.l10n.codeWsSessionSettingsEffort,
            style: TextStyle(
              fontSize: 12,
              fontWeight: FontWeight.w600,
              color: AppTheme.textSecondary,
            ),
          ),
          const SizedBox(width: 12),
          Expanded(
            child: Container(
              padding: const EdgeInsets.all(3),
              decoration: BoxDecoration(
                color: AppTheme.surfaceMuted,
                borderRadius: BorderRadius.circular(10),
                border: Border.all(
                  color: AppTheme.border.withValues(alpha: 0.6),
                  width: 0.7,
                ),
              ),
              child: SingleChildScrollView(
                scrollDirection: Axis.horizontal,
                child: Row(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    _EffortSegmentButton(
                      label: context.l10n.codeWsSessionSettingsEffortDefault,
                      selected: _effort.isEmpty,
                      onTap: () => setEffortValue(''),
                    ),
                    for (final option in effortOptions) ...[
                      const SizedBox(width: 3),
                      _EffortSegmentButton(
                        key: ValueKey('sheet_effort_${option.value}'),
                        label: option.label,
                        selected: _effort == option.value,
                        onTap: () => setEffortValue(option.value),
                      ),
                    ],
                  ],
                ),
              ),
            ),
          ),
        ],
      ),
    );
  }

  Widget _buildDefaultOptionCard(
    BuildContext context, {
    required bool isDefaultSelected,
  }) {
    return Container(
      decoration: BoxDecoration(
        color: AppTheme.surface,
        borderRadius: BorderRadius.circular(16),
        border: Border.all(color: AppTheme.border.withValues(alpha: 0.75)),
      ),
      clipBehavior: Clip.antiAlias,
      child: Material(
        color: isDefaultSelected
            ? AppTheme.primaryBlueLight.withValues(
                alpha: AppTheme.isDark ? 0.36 : 0.6,
              )
            : Colors.transparent,
        child: InkWell(
          key: const ValueKey('code_model_option_default'),
          onTap: () => _selectModel(
            '',
            executorId: widget.selectedExecutor?.id,
          ),
          child: Padding(
            padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 12),
            child: Row(
              children: [
                Icon(
                  Icons.auto_awesome_outlined,
                  size: 18,
                  color: isDefaultSelected
                      ? AppTheme.primary
                      : AppTheme.textSecondary,
                ),
                const SizedBox(width: 12),
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(
                        context.l10n.codeWsSessionSettingsModelHint,
                        style: TextStyle(
                          fontSize: 14,
                          fontWeight: isDefaultSelected
                              ? FontWeight.w700
                              : FontWeight.w600,
                          color: AppTheme.textPrimary,
                        ),
                      ),
                      const SizedBox(height: 2),
                      Text(
                        context.l10n.codeWsModelPickerDefaultSubtitle,
                        style: TextStyle(
                          fontSize: 11.5,
                          color: AppTheme.textSecondary,
                        ),
                      ),
                    ],
                  ),
                ),
                if (isDefaultSelected)
                  Container(
                    width: 20,
                    height: 20,
                    decoration: BoxDecoration(
                      color: AppTheme.primary,
                      shape: BoxShape.circle,
                    ),
                    alignment: Alignment.center,
                    child: const Icon(
                      Icons.check_rounded,
                      size: 13,
                      color: Colors.white,
                    ),
                  ),
              ],
            ),
          ),
        ),
      ),
    );
  }

  Widget _buildSectionHeader(_CodeModelSection section) {
    final vendorColor = VendorColors.forVendor(section.providerName);
    final showExecutorNote =
        section.executorName.isNotEmpty &&
        section.executorName.toLowerCase() !=
            section.providerName.toLowerCase();

    return Padding(
      padding: const EdgeInsets.fromLTRB(4, 0, 4, 7),
      child: Row(
        children: [
          Container(
            width: 6,
            height: 6,
            decoration: BoxDecoration(
              color: vendorColor,
              shape: BoxShape.circle,
            ),
          ),
          const SizedBox(width: 7),
          Text(
            section.providerName,
            style: TextStyle(
              fontSize: 11.5,
              fontWeight: FontWeight.w600,
              color: AppTheme.textSecondary,
              letterSpacing: 0.2,
            ),
          ),
          if (showExecutorNote) ...[
            const SizedBox(width: 6),
            Text(
              '· ${section.executorName}',
              style: TextStyle(
                fontSize: 11,
                fontWeight: FontWeight.w500,
                color: AppTheme.textLight,
              ),
            ),
          ],
          const Spacer(),
          Text(
            '${section.items.length}',
            style: TextStyle(
              fontSize: 11,
              fontWeight: FontWeight.w500,
              color: AppTheme.textLight,
            ),
          ),
        ],
      ),
    );
  }

  Widget _buildSectionCard(
    BuildContext context, {
    required _CodeModelSection section,
    required String currentModelTrimmed,
    required String activeExecId,
  }) {
    final vendorColor = VendorColors.forVendor(section.providerName);

    return Container(
      decoration: BoxDecoration(
        color: AppTheme.surface,
        borderRadius: BorderRadius.circular(16),
        border: Border.all(color: AppTheme.border.withValues(alpha: 0.75)),
        boxShadow: AppTheme.isDark
            ? null
            : [
                BoxShadow(
                  color: AppTheme.textPrimary.withValues(alpha: 0.03),
                  blurRadius: 10,
                  offset: const Offset(0, 2),
                ),
              ],
      ),
      clipBehavior: Clip.antiAlias,
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          for (int i = 0; i < section.items.length; i++) ...[
            if (i > 0)
              Divider(
                height: 1,
                indent: 50,
                endIndent: 16,
                color: AppTheme.border.withValues(alpha: 0.5),
              ),
            _buildModelRow(
              item: section.items[i],
              vendorColor: vendorColor,
              currentModelTrimmed: currentModelTrimmed,
              activeExecId: activeExecId,
            ),
          ],
        ],
      ),
    );
  }

  Widget _buildModelRow({
    required CodeSelectableModel item,
    required Color vendorColor,
    required String currentModelTrimmed,
    required String activeExecId,
  }) {
    final isSelected =
        item.value == currentModelTrimmed &&
        (activeExecId.isEmpty || item.executorId == activeExecId);
    final switchesExecutor =
        activeExecId.isNotEmpty && item.executorId != activeExecId;

    return Material(
      color: isSelected
          ? AppTheme.primaryBlueLight.withValues(
              alpha: AppTheme.isDark ? 0.36 : 0.6,
            )
          : Colors.transparent,
      child: InkWell(
        key: ValueKey('code_model_option_${item.executorId}_${item.value}'),
        onTap: () => _selectModel(item.value, executorId: item.executorId),
        child: Padding(
          padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 11.5),
          child: Row(
            children: [
              Container(
                width: 26,
                height: 26,
                decoration: BoxDecoration(
                  color: vendorColor.withValues(
                    alpha: AppTheme.isDark ? 0.16 : 0.10,
                  ),
                  borderRadius: BorderRadius.circular(7),
                  border: Border.all(
                    color: vendorColor.withValues(alpha: 0.24),
                    width: 0.8,
                  ),
                ),
                alignment: Alignment.center,
                child: Container(
                  width: 6.5,
                  height: 6.5,
                  decoration: BoxDecoration(
                    color: vendorColor,
                    shape: BoxShape.circle,
                  ),
                ),
              ),
              const SizedBox(width: 11),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    Row(
                      children: [
                        Flexible(
                          child: Text(
                            item.displayName,
                            maxLines: 1,
                            overflow: TextOverflow.ellipsis,
                            style: TextStyle(
                              fontSize: 14.5,
                              fontWeight: isSelected
                                  ? FontWeight.w700
                                  : FontWeight.w600,
                              color: AppTheme.textPrimary,
                              letterSpacing: -0.2,
                            ),
                          ),
                        ),
                        if (switchesExecutor) ...[
                          const SizedBox(width: 7),
                          Container(
                            padding: const EdgeInsets.symmetric(
                              horizontal: 6,
                              vertical: 1.5,
                            ),
                            decoration: BoxDecoration(
                              color: AppTheme.primaryBlueLight,
                              borderRadius: BorderRadius.circular(5),
                              border: Border.all(
                                color: AppTheme.primaryBlueBorder,
                                width: 0.6,
                              ),
                            ),
                            child: Text(
                              item.executorName,
                              style: TextStyle(
                                fontSize: 10,
                                fontWeight: FontWeight.w600,
                                color: AppTheme.primaryBlueText,
                              ),
                            ),
                          ),
                        ],
                      ],
                    ),
                    const SizedBox(height: 2.5),
                    Text(
                      item.value,
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: TextStyle(
                        fontSize: 11.5,
                        fontFamily: 'monospace',
                        color: isSelected
                            ? AppTheme.textSecondary
                            : AppTheme.textLight,
                      ),
                    ),
                  ],
                ),
              ),
              const SizedBox(width: 10),
              if (isSelected)
                Container(
                  width: 20,
                  height: 20,
                  decoration: BoxDecoration(
                    color: AppTheme.primary,
                    shape: BoxShape.circle,
                  ),
                  alignment: Alignment.center,
                  child: const Icon(
                    Icons.check_rounded,
                    size: 13,
                    color: Colors.white,
                  ),
                )
              else
                const SizedBox(width: 20, height: 20),
            ],
          ),
        ),
      ),
    );
  }
}

class _ProviderFilterChip extends StatelessWidget {
  const _ProviderFilterChip({
    super.key,
    required this.name,
    required this.count,
    required this.selected,
    required this.onTap,
  });

  final String name;
  final int count;
  final bool selected;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return InkWell(
      borderRadius: BorderRadius.circular(999),
      onTap: onTap,
      child: AnimatedContainer(
        duration: const Duration(milliseconds: 140),
        padding: const EdgeInsets.symmetric(horizontal: 11, vertical: 5.5),
        decoration: BoxDecoration(
          color: selected ? AppTheme.primaryBlueLight : AppTheme.surfaceMuted,
          borderRadius: BorderRadius.circular(999),
          border: Border.all(
            color: selected
                ? AppTheme.primaryBlueBorder
                : AppTheme.border.withValues(alpha: 0.7),
            width: 0.8,
          ),
        ),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Text(
              name,
              style: TextStyle(
                fontSize: 12,
                fontWeight: selected ? FontWeight.w700 : FontWeight.w500,
                color: selected
                    ? AppTheme.primaryBlueText
                    : AppTheme.textSecondary,
              ),
            ),
            const SizedBox(width: 5),
            Text(
              '$count',
              style: TextStyle(
                fontSize: 11,
                fontWeight: FontWeight.w500,
                color: selected
                    ? AppTheme.primaryBlueText.withValues(alpha: 0.75)
                    : AppTheme.textLight,
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _EffortSegmentButton extends StatelessWidget {
  const _EffortSegmentButton({
    super.key,
    required this.label,
    required this.selected,
    required this.onTap,
  });

  final String label;
  final bool selected;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return GestureDetector(
      behavior: HitTestBehavior.opaque,
      onTap: onTap,
      child: AnimatedContainer(
        duration: const Duration(milliseconds: 140),
        padding: const EdgeInsets.symmetric(horizontal: 11, vertical: 5),
        decoration: BoxDecoration(
          color: selected ? AppTheme.surface : Colors.transparent,
          borderRadius: BorderRadius.circular(7),
          border: selected
              ? Border.all(
                  color: AppTheme.border.withValues(alpha: 0.85),
                  width: 0.7,
                )
              : null,
          boxShadow: selected && !AppTheme.isDark
              ? [
                  BoxShadow(
                    color: AppTheme.textPrimary.withValues(alpha: 0.05),
                    blurRadius: 4,
                    offset: const Offset(0, 1),
                  ),
                ]
              : null,
        ),
        child: Text(
          label,
          style: TextStyle(
            fontSize: 11.5,
            fontWeight: selected ? FontWeight.w700 : FontWeight.w500,
            color: selected ? AppTheme.textPrimary : AppTheme.textSecondary,
          ),
        ),
      ),
    );
  }
}
