import 'package:shared_preferences/shared_preferences.dart';

import 'ai_dev_session.dart';

class CodeSelectableModel {
  const CodeSelectableModel({
    required this.value,
    required this.label,
    required this.displayName,
    required this.providerId,
    required this.providerName,
    required this.executorId,
    required this.executorName,
    this.isPresetFallback = false,
  });

  final String value;
  final String label;
  final String displayName;
  final String providerId;
  final String providerName;
  final String executorId;
  final String executorName;
  final bool isPresetFallback;
}

class CodeModelProviderGroup {
  const CodeModelProviderGroup({
    required this.id,
    required this.name,
    required this.count,
    this.isRunner = false,
  });

  final String id;
  final String name;
  final int count;
  final bool isRunner;
}

class CodeModelSelection {
  const CodeModelSelection({
    required this.model,
    this.executorId,
    this.reasoningEffort,
  });

  final String model;
  final String? executorId;
  final String? reasoningEffort;
}

class CodeModelCatalog {
  const CodeModelCatalog._();

  static const List<CodeExecutorOption> defaultReasoningEfforts = [
    CodeExecutorOption(value: 'low', label: 'Low'),
    CodeExecutorOption(value: 'medium', label: 'Medium'),
    CodeExecutorOption(value: 'high', label: 'High'),
    CodeExecutorOption(value: 'xhigh', label: 'XHigh'),
    CodeExecutorOption(value: 'max', label: 'Max'),
  ];

  static const String lastModelPrefKey =
      'hoxi.ai_workspace.last_selected_model_v1';
  static const String lastExecutorPrefKey =
      'hoxi.ai_workspace.last_selected_executor_v1';

  static SharedPreferences? _cachedPrefs;
  static String _lastSelectedModel = '';
  static String _lastSelectedExecutorId = '';

  static void _syncPrefsScope(SharedPreferences prefs) {
    if (!identical(_cachedPrefs, prefs)) {
      _cachedPrefs = prefs;
      _lastSelectedModel = (prefs.getString(lastModelPrefKey) ?? '').trim();
      _lastSelectedExecutorId = (prefs.getString(lastExecutorPrefKey) ?? '')
          .trim();
    }
  }

  static CodeModelSelection? get lastSelection {
    final model = _lastSelectedModel.trim();
    if (model.isEmpty) return null;
    final exec = _lastSelectedExecutorId.trim();
    return CodeModelSelection(
      model: model,
      executorId: exec.isEmpty ? null : exec,
    );
  }

  static Future<CodeModelSelection?> loadLastSelection() async {
    try {
      final prefs = await SharedPreferences.getInstance();
      _syncPrefsScope(prefs);
      final savedModel = (prefs.getString(lastModelPrefKey) ?? '').trim();
      final savedExec = (prefs.getString(lastExecutorPrefKey) ?? '').trim();
      if (savedModel.isNotEmpty) {
        _lastSelectedModel = savedModel;
        _lastSelectedExecutorId = savedExec;
      }
    } catch (_) {}
    return lastSelection;
  }

  static Future<void> saveLastSelection({
    required String model,
    String? executorId,
  }) async {
    final trimmedModel = model.trim();
    if (trimmedModel.isEmpty) return;
    final trimmedExec = (executorId ?? '').trim();
    _lastSelectedModel = trimmedModel;
    if (trimmedExec.isNotEmpty) {
      _lastSelectedExecutorId = trimmedExec;
    }
    try {
      final prefs = await SharedPreferences.getInstance();
      _cachedPrefs = prefs;
      await prefs.setString(lastModelPrefKey, trimmedModel);
      if (trimmedExec.isNotEmpty) {
        await prefs.setString(lastExecutorPrefKey, trimmedExec);
      }
    } catch (_) {}
  }

  static Future<void> clearLastSelection() async {
    _lastSelectedModel = '';
    _lastSelectedExecutorId = '';
    try {
      final prefs = await SharedPreferences.getInstance();
      _cachedPrefs = prefs;
      await prefs.remove(lastModelPrefKey);
      await prefs.remove(lastExecutorPrefKey);
    } catch (_) {}
  }

  static AiDevSession? latestSessionWithModel(
    AiDevSession? currentSession,
    List<AiDevSession> sessions,
  ) {
    if (currentSession != null &&
        currentSession.providerModel.trim().isNotEmpty) {
      return currentSession;
    }
    for (final session in sessions) {
      if (session.providerModel.trim().isNotEmpty) {
        return session;
      }
    }
    return null;
  }

  static const List<CodeExecutorOption> openCodeFallbackModels = [
    CodeExecutorOption(value: 'anthropic/claude-sonnet-4-6', label: 'Anthropic · Claude Sonnet 4.6'),
    CodeExecutorOption(value: 'anthropic/claude-opus-4-6', label: 'Anthropic · Claude Opus 4.6'),
    CodeExecutorOption(value: 'anthropic/claude-sonnet-4-5', label: 'Anthropic · Claude Sonnet 4.5'),
    CodeExecutorOption(value: 'openai/gpt-5.4', label: 'OpenAI · GPT-5.4'),
    CodeExecutorOption(value: 'openai/gpt-5.3-codex', label: 'OpenAI · GPT-5.3 Codex'),
    CodeExecutorOption(value: 'google/gemini-3.1-pro', label: 'Google · Gemini 3.1 Pro'),
    CodeExecutorOption(value: 'google/gemini-2.5-pro', label: 'Google · Gemini 2.5 Pro'),
    CodeExecutorOption(value: 'deepseek/deepseek-chat', label: 'DeepSeek · DeepSeek V3.2'),
    CodeExecutorOption(value: 'deepseek/deepseek-reasoner', label: 'DeepSeek · DeepSeek R1'),
    CodeExecutorOption(value: 'qwen/qwen3-coder-plus', label: 'Qwen · Qwen3 Coder Plus'),
    CodeExecutorOption(value: 'zhipu/glm-5.1', label: 'Zhipu · GLM-5.1'),
    CodeExecutorOption(value: 'moonshot/kimi-k2.5', label: 'Moonshot · Kimi K2.5'),
    CodeExecutorOption(value: 'minimax/minimax-m2.7', label: 'MiniMax · MiniMax M2.7'),
  ];

  static const List<CodeExecutorOption> claudeCodeFallbackModels = [
    CodeExecutorOption(value: 'claude-sonnet-4-6', label: 'Claude Sonnet 4.6'),
    CodeExecutorOption(value: 'claude-opus-4-6', label: 'Claude Opus 4.6'),
    CodeExecutorOption(value: 'claude-sonnet-4-5', label: 'Claude Sonnet 4.5'),
    CodeExecutorOption(value: 'claude-haiku-4-5', label: 'Claude Haiku 4.5'),
  ];

  static const List<CodeExecutorOption> antigravityFallbackModels = [
    CodeExecutorOption(value: 'gemini-3.1-pro-high', label: 'Gemini 3.1 Pro (High)'),
    CodeExecutorOption(value: 'gemini-3.1-pro-low', label: 'Gemini 3.1 Pro (Low)'),
    CodeExecutorOption(value: 'gemini-3-flash', label: 'Gemini 3 Flash'),
    CodeExecutorOption(value: 'claude-sonnet-4-6', label: 'Claude Sonnet 4.6'),
    CodeExecutorOption(value: 'claude-opus-4-6-thinking', label: 'Claude Opus 4.6 (Thinking)'),
    CodeExecutorOption(value: 'gpt-oss-120b-medium', label: 'GPT-OSS 120B (Medium)'),
  ];

  static const List<CodeExecutorOption> geminiCliFallbackModels = [
    CodeExecutorOption(value: 'gemini-3.1-pro-preview', label: 'Gemini 3.1 Pro'),
    CodeExecutorOption(value: 'gemini-2.5-pro', label: 'Gemini 2.5 Pro'),
    CodeExecutorOption(value: 'gemini-2.5-flash', label: 'Gemini 2.5 Flash'),
  ];

  static const List<CodeExecutorOption> codexFallbackModels = [
    CodeExecutorOption(value: 'gpt-5.4', label: 'GPT-5.4'),
    CodeExecutorOption(value: 'gpt-5.3-codex', label: 'GPT-5.3 Codex'),
    CodeExecutorOption(value: 'o4-mini', label: 'o4-mini'),
  ];

  static const List<CodeExecutorOption> qoderFallbackModels = [
    CodeExecutorOption(value: 'auto', label: 'Auto (自动选择)'),
    CodeExecutorOption(value: 'ultimate', label: 'Ultimate (旗舰)'),
    CodeExecutorOption(value: 'efficient', label: 'Efficient (高效)'),
  ];

  static const List<CodeExecutorOption> codeBuddyFallbackModels = [
    CodeExecutorOption(value: 'claude-4.0-sonnet', label: 'Claude 4.0 Sonnet'),
    CodeExecutorOption(value: 'gpt-5', label: 'GPT-5'),
    CodeExecutorOption(value: 'gemini-2.5-pro', label: 'Gemini 2.5 Pro'),
    CodeExecutorOption(value: 'deepseek-v3.1', label: 'DeepSeek V3.1'),
  ];

  static List<CodeExecutorOption> fallbackOptionsForExecutor(
    String executorId,
  ) {
    final normalized = executorId.trim().toLowerCase();
    return switch (normalized) {
      'claude-code' || 'claude' => claudeCodeFallbackModels,
      'antigravity' || 'agy' => antigravityFallbackModels,
      'gemini-cli' || 'gemini' => geminiCliFallbackModels,
      'codex' || 'openai' => codexFallbackModels,
      'qoder' => qoderFallbackModels,
      'codebuddy' => codeBuddyFallbackModels,
      _ => openCodeFallbackModels,
    };
  }

  static List<CodeExecutorOption> effectiveModelOptions(
    CodeExecutor? executor, {
    String currentModel = '',
    String executorIdHint = '',
  }) {
    final raw = executor?.modelOptions ?? const <CodeExecutorOption>[];
    final base = raw.isNotEmpty
        ? raw
        : fallbackOptionsForExecutor(executor?.id ?? executorIdHint);
    final trimmedCurrent = currentModel.trim();
    if (trimmedCurrent.isEmpty ||
        base.any((option) => option.value == trimmedCurrent)) {
      return base;
    }
    return [
      CodeExecutorOption(value: trimmedCurrent, label: trimmedCurrent),
      ...base,
    ];
  }

  static List<CodeExecutorOption> effectiveReasoningEfforts(
    CodeExecutor? executor, {
    String currentEffort = '',
  }) {
    final raw = executor?.reasoningEfforts ?? const <CodeExecutorOption>[];
    final base = raw.isNotEmpty
        ? raw
        : defaultReasoningEfforts;
    final trimmedCurrent = currentEffort.trim();
    if (trimmedCurrent.isEmpty ||
        base.any((option) => option.value == trimmedCurrent)) {
      return base;
    }
    return [
      CodeExecutorOption(value: trimmedCurrent, label: trimmedCurrent),
      ...base,
    ];
  }

  static List<CodeSelectableModel> buildUnifiedCatalog({
    required List<CodeExecutor> executors,
    CodeExecutor? selectedExecutor,
    String currentModel = '',
    String executorIdHint = '',
  }) {
    final availableExecutors = executors
        .where((item) => item.available && item.supportsAutomation)
        .toList();
    final anyLiveModels = availableExecutors.any(
      (item) => item.modelOptions.isNotEmpty,
    );
    final orderedExecutors = <CodeExecutor>[
      if (selectedExecutor != null &&
          (!anyLiveModels || selectedExecutor.modelOptions.isNotEmpty))
        selectedExecutor,
      for (final exec in availableExecutors)
        if (exec.id != selectedExecutor?.id &&
            (!anyLiveModels || exec.modelOptions.isNotEmpty))
          exec,
      if (anyLiveModels)
        for (final exec in availableExecutors)
          if (exec.modelOptions.isEmpty &&
              !orderedContainsId(selectedExecutor, exec.id))
            exec,
    ];

    if (orderedExecutors.isEmpty) {
      final fallbackId = executorIdHint.trim().isEmpty
          ? 'native'
          : executorIdHint.trim();
      final options = effectiveModelOptions(
        null,
        currentModel: currentModel,
        executorIdHint: fallbackId,
      );
      return [
        for (final option in options)
          _toSelectableModel(
            option: option,
            executorId: fallbackId,
            executorName: _defaultExecutorName(fallbackId),
            isPresetFallback: true,
          ),
      ];
    }

    final results = <CodeSelectableModel>[];
    final seen = <String>{};

    for (final executor in orderedExecutors) {
      final isSelectedExec = executor.id == selectedExecutor?.id;
      final isFallback = executor.modelOptions.isEmpty;
      if (isFallback && anyLiveModels) {
        final trimmedCurrent = isSelectedExec ? currentModel.trim() : '';
        if (trimmedCurrent.isNotEmpty) {
          final key = '${executor.id}::$trimmedCurrent';
          if (seen.add(key)) {
            results.add(
              _toSelectableModel(
                option: CodeExecutorOption(
                  value: trimmedCurrent,
                  label: trimmedCurrent,
                ),
                executorId: executor.id,
                executorName: executor.name.trim().isEmpty
                    ? _defaultExecutorName(executor.id)
                    : executor.name.trim(),
                isPresetFallback: true,
              ),
            );
          }
        }
        continue;
      }
      final options = effectiveModelOptions(
        executor,
        currentModel: isSelectedExec ? currentModel : '',
        executorIdHint: executor.id,
      );
      for (final option in options) {
        final key = '${executor.id}::${option.value}';
        if (!seen.add(key)) continue;
        results.add(
          _toSelectableModel(
            option: option,
            executorId: executor.id,
            executorName: executor.name.trim().isEmpty
                ? _defaultExecutorName(executor.id)
                : executor.name.trim(),
            isPresetFallback: isFallback,
          ),
        );
      }
    }

    return results;
  }

  static bool orderedContainsId(CodeExecutor? selected, String id) =>
      selected != null && selected.id == id && selected.modelOptions.isNotEmpty;

  static CodeSelectableModel? defaultModelForExecutors(
    List<CodeExecutor> executors, {
    CodeExecutor? preferredExecutor,
    String executorIdHint = '',
  }) {
    final catalog = buildUnifiedCatalog(
      executors: executors,
      selectedExecutor: preferredExecutor,
      executorIdHint: executorIdHint,
    );
    if (catalog.isEmpty) return null;
    for (final item in catalog) {
      if (!item.isPresetFallback && item.providerId != 'opencode') {
        return item;
      }
    }
    for (final item in catalog) {
      if (!item.isPresetFallback) return item;
    }
    return catalog.first;
  }

  /// 新建会话时优先复用上一次会话选择的模型（若在当前可用执行器中存在或为自定义模型），否则回退默认。
  static CodeSelectableModel? resolvePreferredModelForExecutors(
    List<CodeExecutor> executors, {
    CodeExecutor? preferredExecutor,
    String executorIdHint = '',
    AiDevSession? fallbackSession,
  }) {
    final availableExecutors = executors
        .where((item) => item.available && item.supportsAutomation)
        .toList();
    final anyLiveModels = availableExecutors.any(
      (item) => item.modelOptions.isNotEmpty,
    );
    final usableExecutors = anyLiveModels
        ? availableExecutors.where((e) => e.modelOptions.isNotEmpty).toList()
        : availableExecutors;

    final candidates = <(String, String)>[
      if (_lastSelectedModel.trim().isNotEmpty)
        (_lastSelectedModel.trim(), _lastSelectedExecutorId.trim()),
      if (fallbackSession != null &&
          fallbackSession.providerModel.trim().isNotEmpty)
        (
          fallbackSession.providerModel.trim(),
          fallbackSession.agentName.trim(),
        ),
    ];

    for (final (candidateModel, candidateExecId) in candidates) {
      final matched = _matchCandidateModel(
        model: candidateModel,
        executorId: candidateExecId,
        usableExecutors: usableExecutors,
        preferredExecutor: preferredExecutor,
      );
      if (matched != null) return matched;
    }

    return defaultModelForExecutors(
      executors,
      preferredExecutor: preferredExecutor,
      executorIdHint: executorIdHint,
    );
  }

  static CodeSelectableModel? _matchCandidateModel({
    required String model,
    required String executorId,
    required List<CodeExecutor> usableExecutors,
    CodeExecutor? preferredExecutor,
  }) {
    final trimmed = model.trim();
    final cleanExecId = executorId.trim();
    if (trimmed.isEmpty || usableExecutors.isEmpty) return null;
    final matchedUsableExec = cleanExecId.isEmpty
        ? null
        : usableExecutors.cast<CodeExecutor?>().firstWhere(
            (e) => e?.id == cleanExecId,
            orElse: () => null,
          );
    final selectedExec = matchedUsableExec ??
        (preferredExecutor != null &&
                usableExecutors.any((e) => e.id == preferredExecutor.id)
            ? preferredExecutor
            : null);
    final catalog = buildUnifiedCatalog(
      executors: usableExecutors,
      selectedExecutor: selectedExec,
      executorIdHint: cleanExecId,
    );
    if (cleanExecId.isNotEmpty) {
      for (final item in catalog) {
        if (item.executorId == cleanExecId && item.value == trimmed) return item;
      }
    }
    for (final item in catalog) {
      if (item.value == trimmed) return item;
    }
    if (matchedUsableExec != null && !_isBuiltInPresetModel(trimmed)) {
      return findSelectedModel(
        executors: usableExecutors,
        selectedExecutor: matchedUsableExec,
        modelValue: trimmed,
        executorIdHint: matchedUsableExec.id,
      );
    }
    return null;
  }

  static bool _isBuiltInPresetModel(String model) {
    for (final list in const [
      openCodeFallbackModels, claudeCodeFallbackModels, antigravityFallbackModels,
      geminiCliFallbackModels, codexFallbackModels, qoderFallbackModels, codeBuddyFallbackModels,
    ]) {
      if (list.any((option) => option.value == model)) return true;
    }
    return false;
  }

  static bool isCoreEngine(String executorId) {
    final normalized = executorId.trim().toLowerCase();
    return normalized == 'native' || normalized == 'opencode';
  }

  static String preferredCoreRuntime(
    List<CodeExecutor> executors, {
    String fallback = 'opencode',
  }) {
    if (executors.any((e) => e.id == 'native')) return 'native';
    if (executors.any((e) => e.id == 'opencode')) return 'opencode';
    final clean = fallback.trim().toLowerCase();
    return isCoreEngine(clean) ? clean : 'opencode';
  }

  /// 根据所选模型与当前可用执行器推导目标运行时（对齐桌面端 resolveRunnerRuntimeForProvider）。
  static String resolveRuntimeForModel({
    required String model,
    String? explicitExecutorId,
    List<CodeExecutor> executors = const [],
    String fallbackRuntime = 'opencode',
    bool openCodeDaemonReady = false,
  }) {
    final trimmedModel = model.trim();
    final explicit = (explicitExecutorId ?? '').trim();
    final cleanFallback = fallbackRuntime.trim().isEmpty
        ? preferredCoreRuntime(executors)
        : fallbackRuntime.trim();

    if (trimmedModel.isNotEmpty) {
      final slashIdx = trimmedModel.indexOf('/');
      final hasSlash = slashIdx > 0 && slashIdx < trimmedModel.length - 1;
      final slashPrefix = hasSlash
          ? trimmedModel.substring(0, slashIdx).trim().toLowerCase()
          : '';
      if (explicit.isNotEmpty) {
        final explicitMatch = executors.any(
          (e) =>
              e.id == explicit &&
              effectiveModelOptions(e).any(
                (o) => o.value.trim() == trimmedModel,
              ),
        );
        if (explicitMatch) return _canonicalRunnerId(explicit);
        if (!isCoreEngine(explicit) &&
            _isRunnerExecutor(explicit) &&
            !trimmedModel.contains('/')) {
          return _canonicalRunnerId(explicit);
        }
        if (isCoreEngine(explicit) && hasSlash && !_isRunnerExecutor(slashPrefix)) {
          final canonicalExplicit = _canonicalRunnerId(explicit);
          final hasExplicitExec = executors.any((e) => e.id == canonicalExplicit);
          final hasOtherCoreExec = executors.any(
            (e) => isCoreEngine(e.id) && e.id != canonicalExplicit,
          );
          if (canonicalExplicit == 'native' ||
              hasExplicitExec ||
              !hasOtherCoreExec ||
              (canonicalExplicit == 'opencode' && openCodeDaemonReady)) {
            return canonicalExplicit;
          }
        }
      }
      final fallbackMatch = executors.any(
        (e) =>
            e.id == cleanFallback &&
            effectiveModelOptions(e).any((o) => o.value.trim() == trimmedModel),
      );
      if (fallbackMatch) return _canonicalRunnerId(cleanFallback);
      for (final exec in executors) {
        if (exec.modelOptions.any((o) => o.value.trim() == trimmedModel)) {
          return _canonicalRunnerId(exec.id);
        }
      }

      if (hasSlash) {
        if (_isRunnerExecutor(slashPrefix)) {
          final inCore = executors.any(
            (e) =>
                isCoreEngine(e.id) &&
                e.modelOptions.any((o) => o.value.trim() == trimmedModel),
          );
          if (!inCore) return _canonicalRunnerId(slashPrefix);
        }
        final fallbackCore = isCoreEngine(explicit)
            ? explicit
            : (isCoreEngine(cleanFallback) ? cleanFallback : 'opencode');
        return preferredCoreRuntime(executors, fallback: fallbackCore);
      }

      if (trimmedModel.toLowerCase() == 'auto') {
        if (explicit.isNotEmpty) return _canonicalRunnerId(explicit);
        return _canonicalRunnerId(cleanFallback);
      }
      if (explicit.isNotEmpty &&
          (!isCoreEngine(explicit) || explicit == 'native')) {
        return _canonicalRunnerId(explicit);
      }
      if (!isCoreEngine(cleanFallback) && _isRunnerExecutor(cleanFallback)) {
        return _canonicalRunnerId(cleanFallback);
      }
      for (final runnerId in const [
        'antigravity',
        'claude-code',
        'gemini-cli',
        'qoder',
        'codebuddy',
        'codex',
      ]) {
        if (executors.isNotEmpty &&
            !executors.any((e) => e.id == runnerId && e.available)) {
          continue;
        }
        if (fallbackOptionsForExecutor(
          runnerId,
        ).any((o) => o.value.trim() == trimmedModel)) {
          return runnerId;
        }
      }
    }

    if (explicit.isNotEmpty) return _canonicalRunnerId(explicit);
    return _canonicalRunnerId(cleanFallback);
  }

  static String _canonicalRunnerId(String raw) {
    return switch (raw.trim().toLowerCase()) {
      'native' => 'native',
      'opencode' => 'opencode',
      'claude' || 'claude-code' => 'claude-code',
      'agy' || 'antigravity' => 'antigravity',
      'gemini' || 'gemini-cli' => 'gemini-cli',
      _ => raw.trim(),
    };
  }

  static List<CodeModelProviderGroup> providerGroups(
    List<CodeSelectableModel> models,
  ) {
    final counts = <String, int>{};
    final names = <String, String>{};
    final runnerFlags = <String, bool>{};
    final hasLiveModel = <String, bool>{};
    for (final item in models) {
      counts[item.providerId] = (counts[item.providerId] ?? 0) + 1;
      names.putIfAbsent(item.providerId, () => item.providerName);
      runnerFlags.putIfAbsent(
        item.providerId,
        () => _isRunnerExecutor(item.executorId),
      );
      if (!item.isPresetFallback) {
        hasLiveModel[item.providerId] = true;
      } else {
        hasLiveModel.putIfAbsent(item.providerId, () => false);
      }
    }
    final keys = counts.keys.toList();
    keys.sort((a, b) {
      final aLive = hasLiveModel[a] ?? false;
      final bLive = hasLiveModel[b] ?? false;
      if (aLive != bLive) return aLive ? -1 : 1;
      return 0;
    });
    return keys
        .map(
          (id) => CodeModelProviderGroup(
            id: id,
            name: names[id] ?? id,
            count: counts[id] ?? 0,
            isRunner: runnerFlags[id] ?? false,
          ),
        )
        .toList();
  }

  static CodeSelectableModel? findSelectedModel({
    required List<CodeExecutor> executors,
    CodeExecutor? selectedExecutor,
    required String modelValue,
    String executorIdHint = '',
  }) {
    final trimmed = modelValue.trim();
    if (trimmed.isEmpty) return null;
    final catalog = buildUnifiedCatalog(
      executors: executors,
      selectedExecutor: selectedExecutor,
      currentModel: trimmed,
      executorIdHint: executorIdHint,
    );
    final activeExecId = selectedExecutor?.id ?? executorIdHint.trim();
    for (final item in catalog) {
      if (item.executorId == activeExecId && item.value == trimmed) {
        return item;
      }
    }
    for (final item in catalog) {
      if (item.value == trimmed) return item;
    }
    return null;
  }

  static CodeSelectableModel _toSelectableModel({
    required CodeExecutorOption option,
    required String executorId,
    required String executorName,
    required bool isPresetFallback,
  }) {
    final rawValue = option.value.trim();
    final rawLabel = option.label.trim().isEmpty
        ? rawValue
        : option.label.trim();
    final isRunner = _isRunnerExecutor(executorId);

    String providerId = executorId;
    String providerName = executorName;
    String displayName = rawLabel;

    if (rawLabel.contains(' · ')) {
      final parts = rawLabel.split(' · ');
      final leading = parts.first.trim();
      final rest = parts.sublist(1).join(' · ').trim();
      if (leading.isNotEmpty && rest.isNotEmpty) {
        providerName = leading;
        displayName = rest;
      }
    }

    if (!isRunner && rawValue.contains('/')) {
      final slashIdx = rawValue.indexOf('/');
      final prefix = rawValue.substring(0, slashIdx).trim();
      if (prefix.isNotEmpty) {
        providerId = prefix.toLowerCase();
        if (!rawLabel.contains(' · ')) {
          providerName = _prettyProviderName(prefix);
        }
      }
    } else if (!isRunner && rawLabel.contains(' · ')) {
      providerId = providerName.toLowerCase().replaceAll(' ', '-');
    }

    return CodeSelectableModel(
      value: rawValue,
      label: rawLabel,
      displayName: displayName,
      providerId: providerId,
      providerName: providerName,
      executorId: executorId,
      executorName: executorName,
      isPresetFallback: isPresetFallback,
    );
  }

  static bool isRunnerExecutor(String executorId) =>
      _isRunnerExecutor(executorId);

  static bool _isRunnerExecutor(String executorId) {
    return switch (executorId.trim().toLowerCase()) {
      'claude-code' ||
      'claude' ||
      'antigravity' ||
      'agy' ||
      'gemini-cli' ||
      'gemini' ||
      'codex' ||
      'qoder' ||
      'codebuddy' => true,
      _ => false,
    };
  }

  static String _defaultExecutorName(String executorId) {
    return switch (executorId.trim().toLowerCase()) {
      'native' => 'Hoxi Native',
      'opencode' => 'OpenCode',
      'claude-code' || 'claude' => 'Claude Code',
      'antigravity' || 'agy' => 'Antigravity',
      'gemini-cli' || 'gemini' => 'Gemini CLI',
      'codex' => 'Codex',
      'qoder' => 'Qoder',
      'codebuddy' => 'CodeBuddy',
      _ => executorId,
    };
  }

  static String _prettyProviderName(String providerId) {
    return switch (providerId.trim().toLowerCase()) {
      'anthropic' => 'Anthropic',
      'openai' => 'OpenAI',
      'google' || 'gemini' => 'Google',
      'deepseek' => 'DeepSeek',
      'qwen' || 'dashscope' || 'alibaba' => 'Qwen',
      'zhipu' || 'glm' || 'bigmodel' => 'Zhipu',
      'moonshot' || 'kimi' => 'Moonshot',
      'minimax' => 'MiniMax',
      'openrouter' => 'OpenRouter',
      'hoxi-official' || 'hoxi' => 'Hoxi Official',
      'opencode' => 'OpenCode',
      _ => providerId,
    };
  }
}
