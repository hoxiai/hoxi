import 'package:flutter/foundation.dart';

import '../../data/ai_workspace_repository.dart';
import '../../data/hoxi_request_ledger_helper.dart';
import '../../models/code_git_review.dart';

class CodeGitReviewState {
  final bool isLoading;
  final bool isSaving;
  final String? errorMessage;
  final CodeGitStatus? status;

  const CodeGitReviewState({
    this.isLoading = false,
    this.isSaving = false,
    this.errorMessage,
    this.status,
  });
}

class CodeGitReviewController extends ChangeNotifier {
  CodeGitReviewController({
    required AiWorkspaceRepository repository,
    required this.sessionId,
  }) : _repository = repository;

  final AiWorkspaceRepository _repository;
  final int sessionId;
  CodeGitReviewState _state = const CodeGitReviewState();
  int _requestVersion = 0;
  bool _disposed = false;
  String? _pendingCommitMessage;
  String? _pendingCommitRequestId;

  CodeGitReviewState get state => _state;

  Future<void> load() async {
    final request = ++_requestVersion;
    _setState(
      CodeGitReviewState(
        isLoading: true,
        isSaving: _state.isSaving,
        status: _state.status,
      ),
    );
    try {
      final status = await _repository.getGitStatus(sessionId);
      if (request != _requestVersion) return;
      if (!status.hasChanges) {
        _pendingCommitMessage = null;
        _pendingCommitRequestId = null;
      }
      _setState(CodeGitReviewState(status: status));
    } catch (error) {
      if (request != _requestVersion) return;
      _setState(
        CodeGitReviewState(
          isSaving: _state.isSaving,
          errorMessage: error.toString(),
          status: _state.status,
        ),
      );
    }
  }

  Future<CodeGitSaveResult?> save(String message) async {
    if (_state.isSaving || _state.status?.hasChanges != true) return null;
    final normalizedMsg = message.trim();
    final reqId = (_pendingCommitMessage == normalizedMsg &&
            _pendingCommitRequestId != null &&
            _pendingCommitRequestId!.isNotEmpty)
        ? _pendingCommitRequestId!
        : HoxiRequestLedgerHelper.createClientRequestId('git-save');
    _pendingCommitMessage = normalizedMsg;
    _pendingCommitRequestId = reqId;

    _setState(CodeGitReviewState(isSaving: true, status: _state.status));
    try {
      final result = await _repository.saveGitChanges(
        sessionId,
        normalizedMsg,
        clientRequestId: reqId,
      );
      _pendingCommitMessage = null;
      _pendingCommitRequestId = null;
      final status = await _repository.getGitStatus(sessionId);
      _setState(CodeGitReviewState(status: status));
      return result;
    } catch (error) {
      if (error is HoxiRequestOutcomeUnknownException) {
        _pendingCommitMessage = null;
        _pendingCommitRequestId = null;
      }
      _setState(
        CodeGitReviewState(
          errorMessage: error.toString(),
          status: _state.status,
        ),
      );
      return null;
    }
  }

  void _setState(CodeGitReviewState next) {
    if (_disposed) return;
    _state = next;
    notifyListeners();
  }

  @override
  void dispose() {
    _disposed = true;
    _requestVersion++;
    super.dispose();
  }
}
