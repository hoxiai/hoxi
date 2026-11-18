import 'package:flutter/material.dart';

import '../../../../core/l10n/l10n.dart';
import '../../../../core/theme/app_theme.dart';
import '../../models/ai_dev_session.dart';

class CodeProjectSelectorField extends StatefulWidget {
  const CodeProjectSelectorField({
    super.key,
    required this.projects,
    required this.selectedProjectId,
    required this.onChanged,
  });

  final List<CodeProject> projects;
  final int? selectedProjectId;
  final ValueChanged<int?> onChanged;

  @override
  State<CodeProjectSelectorField> createState() =>
      _CodeProjectSelectorFieldState();
}

class _CodeProjectSelectorFieldState extends State<CodeProjectSelectorField> {
  final _searchController = TextEditingController();
  final _searchFocusNode = FocusNode();
  bool _isOpen = false;

  @override
  void dispose() {
    _searchController.dispose();
    _searchFocusNode.dispose();
    super.dispose();
  }

  CodeProject? _resolveSelectedProject() {
    final id = widget.selectedProjectId;
    if (id != null) {
      for (final project in widget.projects) {
        if (project.id == id) return project;
      }
    }
    return widget.projects.firstOrNull;
  }

  void _toggleDropdown() {
    setState(() {
      _isOpen = !_isOpen;
      if (!_isOpen) {
        _searchController.clear();
        _searchFocusNode.unfocus();
      }
    });
  }

  void _selectProject(CodeProject project) {
    setState(() {
      _isOpen = false;
      _searchController.clear();
      _searchFocusNode.unfocus();
    });
    widget.onChanged(project.id);
  }

  @override
  Widget build(BuildContext context) {
    final selectedProject = _resolveSelectedProject();
    final query = _searchController.text.trim().toLowerCase();
    final filteredProjects = query.isEmpty
        ? widget.projects
        : widget.projects.where((project) {
            return project.name.toLowerCase().contains(query) ||
                project.workDir.toLowerCase().contains(query) ||
                project.description.toLowerCase().contains(query);
          }).toList();

    return Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        InkWell(
          key: const ValueKey('code_project_selector_field'),
          borderRadius: BorderRadius.circular(14),
          onTap: _toggleDropdown,
          child: InputDecorator(
            decoration: InputDecoration(
              labelText: context.l10n.codeWsHubProject,
              border: const OutlineInputBorder(),
              contentPadding: const EdgeInsets.symmetric(
                horizontal: 14,
                vertical: 12,
              ),
            ),
            child: Row(
              children: [
                Expanded(
                  child: Text(
                    selectedProject?.name ?? '',
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: TextStyle(
                      color: AppTheme.textPrimary,
                      fontSize: 14,
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                ),
                const SizedBox(width: 6),
                Icon(
                  _isOpen
                      ? Icons.keyboard_arrow_up_rounded
                      : Icons.keyboard_arrow_down_rounded,
                  size: 20,
                  color: AppTheme.textSecondary,
                ),
              ],
            ),
          ),
        ),
        if (_isOpen)
          Container(
            key: const ValueKey('code_project_dropdown_panel'),
            margin: const EdgeInsets.only(top: 6),
            decoration: BoxDecoration(
              color: AppTheme.surface,
              borderRadius: BorderRadius.circular(14),
              border: Border.all(
                color: AppTheme.border.withValues(alpha: 0.85),
              ),
              boxShadow: [
                BoxShadow(
                  color: Colors.black.withValues(
                    alpha: AppTheme.isDark ? 0.25 : 0.06,
                  ),
                  blurRadius: 12,
                  offset: const Offset(0, 4),
                ),
              ],
            ),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                Padding(
                  padding: const EdgeInsets.fromLTRB(10, 10, 10, 6),
                  child: TextField(
                    key: const ValueKey('code_project_search_field'),
                    controller: _searchController,
                    focusNode: _searchFocusNode,
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
                              key: const ValueKey('code_project_search_clear'),
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
                      hintText: context.l10n.codeWsProjectPickerSearchHint,
                      hintStyle: TextStyle(
                        fontSize: 13,
                        color: AppTheme.textLight,
                      ),
                      border: OutlineInputBorder(
                        borderRadius: BorderRadius.circular(10),
                        borderSide: BorderSide(
                          color: AppTheme.border.withValues(alpha: 0.65),
                        ),
                      ),
                      enabledBorder: OutlineInputBorder(
                        borderRadius: BorderRadius.circular(10),
                        borderSide: BorderSide(
                          color: AppTheme.border.withValues(alpha: 0.65),
                        ),
                      ),
                      focusedBorder: OutlineInputBorder(
                        borderRadius: BorderRadius.circular(10),
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
                ),
                if (filteredProjects.isEmpty)
                  Padding(
                    padding: const EdgeInsets.symmetric(
                      horizontal: 14,
                      vertical: 20,
                    ),
                    child: Center(
                      child: Text(
                        context.l10n.codeWsProjectPickerEmpty,
                        style: TextStyle(
                          fontSize: 13,
                          color: AppTheme.textSecondary,
                        ),
                      ),
                    ),
                  )
                else
                  ConstrainedBox(
                    constraints: const BoxConstraints(maxHeight: 220),
                    child: ListView.separated(
                      shrinkWrap: true,
                      padding: const EdgeInsets.fromLTRB(6, 2, 6, 8),
                      itemCount: filteredProjects.length,
                      separatorBuilder: (_, _) => const SizedBox(height: 2),
                      itemBuilder: (context, index) {
                        final project = filteredProjects[index];
                        final isSelected = project.id == selectedProject?.id;
                        final subtitle = project.description.trim().isNotEmpty
                            ? project.description.trim()
                            : project.workDir.trim();
                        final showSubtitle =
                            subtitle.isNotEmpty &&
                            subtitle != project.name.trim();
                        return InkWell(
                          key: ValueKey('code_project_option_${project.id}'),
                          borderRadius: BorderRadius.circular(10),
                          onTap: () => _selectProject(project),
                          child: Container(
                            padding: const EdgeInsets.symmetric(
                              horizontal: 10,
                              vertical: 9,
                            ),
                            decoration: BoxDecoration(
                              color: isSelected
                                  ? AppTheme.primaryBlueLight.withValues(
                                      alpha: AppTheme.isDark ? 0.25 : 0.65,
                                    )
                                  : Colors.transparent,
                              borderRadius: BorderRadius.circular(10),
                            ),
                            child: Row(
                              children: [
                                Expanded(
                                  child: Column(
                                    crossAxisAlignment:
                                        CrossAxisAlignment.start,
                                    mainAxisSize: MainAxisSize.min,
                                    children: [
                                      Text(
                                        project.name,
                                        maxLines: 1,
                                        overflow: TextOverflow.ellipsis,
                                        style: TextStyle(
                                          color: AppTheme.textPrimary,
                                          fontSize: 13.5,
                                          fontWeight: isSelected
                                              ? FontWeight.w700
                                              : FontWeight.w500,
                                        ),
                                      ),
                                      if (showSubtitle) ...[
                                        const SizedBox(height: 2),
                                        Text(
                                          subtitle,
                                          maxLines: 1,
                                          overflow: TextOverflow.ellipsis,
                                          style: TextStyle(
                                            color: AppTheme.textSecondary,
                                            fontSize: 11.5,
                                            fontFamily: 'monospace',
                                          ),
                                        ),
                                      ],
                                    ],
                                  ),
                                ),
                                if (isSelected) ...[
                                  const SizedBox(width: 8),
                                  Icon(
                                    Icons.check_rounded,
                                    size: 18,
                                    color: AppTheme.primary,
                                  ),
                                ],
                              ],
                            ),
                          ),
                        );
                      },
                    ),
                  ),
              ],
            ),
          ),
      ],
    );
  }
}
