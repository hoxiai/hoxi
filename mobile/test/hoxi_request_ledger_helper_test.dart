import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:hoxi/core/models/node_action_outcome.dart';
import 'package:hoxi/core/network/api_client.dart';
import 'package:hoxi/core/network/api_exception.dart';
import 'package:hoxi/core/network/api_response.dart';
import 'package:hoxi/features/ai_workspace/data/ai_workspace_repository.dart';
import 'package:hoxi/features/ai_workspace/data/hoxi_git_artifact_adapter.dart';
import 'package:hoxi/features/ai_workspace/data/hoxi_request_ledger_helper.dart';
import 'package:hoxi/features/ai_workspace/models/code_git_review.dart';
import 'package:hoxi/features/ai_workspace/presentation/controllers/code_git_review_controller.dart';

class _StubApiClient implements ApiClient {
  Future<ApiResponse<dynamic>> Function(
    String path,
    Map<String, dynamic>? queryParameters,
    Options? options,
  )? onGet;
  Future<ApiResponse<dynamic>> Function(
    String path,
    dynamic data,
    Map<String, dynamic>? queryParameters,
    Options? options,
  )? onPost;

  @override
  Future<ApiResponse<T>> get<T>(
    String path, {
    Map<String, dynamic>? queryParameters,
    Options? options,
  }) async {
    final handler = onGet;
    if (handler == null) throw ApiException(code: 404, msg: 'unexpected GET $path');
    final res = await handler(path, queryParameters, options);
    return ApiResponse<T>(code: res.code, msg: res.msg, data: res.data as T?);
  }

  @override
  Future<ApiResponse<T>> post<T>(
    String path, {
    dynamic data,
    Map<String, dynamic>? queryParameters,
    Options? options,
  }) async {
    final handler = onPost;
    if (handler == null) throw ApiException(code: 404, msg: 'unexpected POST $path');
    final res = await handler(path, data, queryParameters, options);
    return ApiResponse<T>(code: res.code, msg: res.msg, data: res.data as T?);
  }

  @override
  dynamic noSuchMethod(Invocation invocation) => super.noSuchMethod(invocation);
}

class _TrackingGitRepository extends AiWorkspaceRepository {
  _TrackingGitRepository() : super(ApiClient());

  final List<String> seenRequestIds = <String>[];
  Object? nextSaveError;
  bool hasChanges = true;

  @override
  Future<CodeGitStatus> getGitStatus(int sessionId) async {
    return CodeGitStatus.fromJson({
      'available': true,
      'files': hasChanges ? 1 : 0,
      'staged': 0,
      'changed': hasChanges ? 1 : 0,
      'untracked': 0,
      'additions': hasChanges ? 3 : 0,
      'deletions': 0,
      'stagedAdditions': 0,
      'stagedDeletions': 0,
      'repositories': const [],
    });
  }

  @override
  Future<CodeGitSaveResult> saveGitChanges(
    int sessionId,
    String message, {
    String? clientRequestId,
  }) async {
    seenRequestIds.add(clientRequestId ?? '');
    if (nextSaveError != null) {
      final err = nextSaveError!;
      nextSaveError = null;
      throw err;
    }
    hasChanges = false;
    return CodeGitSaveResult.fromJson({
      'status': 'saved',
      'commit': '9f8e7d6',
      'branch': 'main',
    });
  }
}

void main() {
  const sessionRef = HoxiSessionTarget(
    hoxiId: 'ses_mob_1',
    directory: '/repo',
    projectDir: '/repo',
    branch: 'main',
    sandboxMode: 'local',
    runtime: 'native',
  );

  test(
    'recovers completed commit via GET /api/hoxi/requests/{id} when POST drops before receipt',
    () async {
      final api = _StubApiClient();
      var postCount = 0;
      String? capturedReqId;

      api.onPost = (path, data, query, options) async {
        postCount++;
        final map = (data as Map).cast<String, dynamic>();
        capturedReqId = map['clientRequestId']?.toString();
        throw ApiException(code: -1, msg: '接收响应超时');
      };
      api.onGet = (path, query, options) async {
        expect(path, '/api/hoxi/requests/$capturedReqId');
        expect(query?['kind'], 'worktree_commit');
        expect(query?['scope'], 'ses_mob_1');
        return ApiResponse<dynamic>(
          code: 200,
          msg: 'ok',
          data: <String, dynamic>{
            'ok': true,
            'kind': 'worktree_commit',
            'scope': 'ses_mob_1',
            'clientRequestId': capturedReqId,
            'status': 'completed',
            'result': <String, dynamic>{
              'ok': true,
              'commit': 'c0ffee1',
              'branch': 'main',
            },
          },
        );
      };

      final saved = await HoxiRequestLedgerHelper.commitWorktreeWithRecovery(
        api,
        ref: sessionRef,
        message: 'fix: mobile commit disconnect',
        clientRequestId: 'mob-req-001',
        inspectRetryDelay: Duration.zero,
      );

      expect(postCount, 1);
      expect(saved.commit, 'c0ffee1');
      expect(saved.status, 'saved');
    },
  );

  test(
    'surfaces HoxiRequestOutcomeUnknownException with git HEAD evidence and maps to NodeActionOutcome.unknown',
    () async {
      final api = _StubApiClient();
      api.onPost = (path, data, query, options) async {
        throw ApiException(
          code: 409,
          msg: 'request was interrupted by a Hoxi restart',
          data: const <String, dynamic>{'code': 'outcome_unknown'},
        );
      };
      api.onGet = (path, query, options) async {
        return ApiResponse<dynamic>(
          code: 200,
          msg: 'ok',
          data: const <String, dynamic>{
            'ok': true,
            'kind': 'worktree_commit',
            'scope': 'ses_mob_1',
            'clientRequestId': 'mob-crash-001',
            'status': 'outcome_unknown',
            'errorCode': 'outcome_unknown',
            'evidence': <String, dynamic>{
              'headAdvanced': true,
              'workingTreeClean': true,
            },
          },
        );
      };

      late Object caught;
      try {
        await HoxiRequestLedgerHelper.commitWorktreeWithRecovery(
          api,
          ref: sessionRef,
          message: 'fix: crash recovery',
          clientRequestId: 'mob-crash-001',
          inspectRetryDelay: Duration.zero,
        );
        fail('expected HoxiRequestOutcomeUnknownException');
      } catch (e) {
        caught = e;
      }

      expect(caught, isA<HoxiRequestOutcomeUnknownException>());
      expect(caught.toString(), contains('Git HEAD 已推进'));
      expect(nodeActionOutcomeFromError(caught), NodeActionOutcome.unknown);
    },
  );

  test(
    'CodeGitReviewController reuses sticky clientRequestId across retry of identical message and mints fresh ID when message changes',
    () async {
      final repo = _TrackingGitRepository();
      final controller = CodeGitReviewController(repository: repo, sessionId: 7);
      addTearDown(controller.dispose);
      await controller.load();

      // 1. Transient failure on "feat: one" -> second tap with "feat: one" reuses exact same clientRequestId.
      repo.nextSaveError = ApiException(code: -1, msg: '网络连接异常');
      expect(await controller.save('feat: one'), isNull);
      expect(repo.seenRequestIds, hasLength(1));
      final firstId = repo.seenRequestIds.single;
      expect(firstId, isNotEmpty);

      // Retry with same message succeeds using identical clientRequestId.
      final res1 = await controller.save('feat: one');
      expect(res1?.commit, '9f8e7d6');
      expect(repo.seenRequestIds, [firstId, firstId]);

      // 2. New dirty state: failure on "feat: two", then user edits message to "feat: three" -> mints fresh ID!
      repo.hasChanges = true;
      await controller.load();
      repo.nextSaveError = ApiException(code: -1, msg: '网络连接异常');
      expect(await controller.save('feat: two'), isNull);
      final twoId = repo.seenRequestIds[2];
      expect(twoId, isNot(equals(firstId)));

      final res2 = await controller.save('feat: three');
      expect(res2?.commit, '9f8e7d6');
      final threeId = repo.seenRequestIds[3];
      expect(threeId, isNot(equals(twoId)));
    },
  );

  test(
    'HoxiSessionDiffScope.isRunnerSessionId recognizes native and codex runner sessions',
    () {
      expect(
        HoxiSessionDiffScope.isRunnerSessionId('ses_nat_1760000000_abcd12'),
        isTrue,
      );
      expect(
        HoxiSessionDiffScope.isRunnerSessionId('ses_cdx_1760000000_abcd12'),
        isTrue,
      );
      expect(
        HoxiSessionDiffScope.isRunnerSessionId('custom_id', runtime: 'native'),
        isTrue,
      );
      expect(
        HoxiSessionDiffScope.isRunnerSessionId('custom_id', runtime: 'codex'),
        isTrue,
      );
    },
  );
}
