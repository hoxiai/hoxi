import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:hoxi/core/network/api_client.dart';
import 'package:hoxi/features/ai_workspace/data/ai_workspace_repository.dart';
import 'package:hoxi/features/ai_workspace/models/code_delivery_job.dart';
import 'package:hoxi/features/ai_workspace/models/code_session_flow.dart';
import 'package:hoxi/features/ai_workspace/presentation/controllers/ai_workspace_controller.dart';
import 'package:hoxi/features/ai_workspace/presentation/widgets/code_flow_card.dart';
import 'package:hoxi/features/task_center/data/task_repository.dart';
import 'package:hoxi/features/website/data/website_repository.dart';

class _FakeFlowRepository extends AiWorkspaceRepository {
  _FakeFlowRepository(this.flow) : super(ApiClient());

  CodeSessionFlow flow;
  final deployed = <(int, int)>[];
  var previewStarts = 0;
  var loads = 0;
  Object? startError;

  @override
  Future<CodeSessionFlow> getSessionFlow(int sessionId) async {
    loads++;
    return flow;
  }

  @override
  Future<void> deployFlowRun(int runId, int environmentId) async {
    deployed.add((runId, environmentId));
  }

  @override
  Future<void> startSessionFlowPreview(int sessionId) async {
    previewStarts++;
    if (startError != null) throw startError!;
  }
}

Map<String, dynamic> _waitingRun() => {
  'id': 41,
  'version': '1.0.3',
  'status': 'waiting_deployment',
  'currentStage': 'waiting_deployment',
  'previewUrl': 'http://preview.example.com',
  'deployable': true,
  'environments': [
    {
      'id': 5,
      'name': 'preview',
      'websiteName': 'preview.example.com',
      'state': 'deployed',
      'current': true,
    },
    {
      'id': 6,
      'name': 'production',
      'websiteName': 'www.example.com',
      'state': 'pending',
      'current': false,
    },
  ],
};

Future<void> _pumpCard(
  WidgetTester tester,
  _FakeFlowRepository repository,
) async {
  tester.binding.platformDispatcher.localeTestValue = const Locale('en');
  addTearDown(tester.binding.platformDispatcher.clearLocaleTestValue);
  await tester.pumpWidget(
    ProviderScope(
      overrides: [aiWorkspaceRepositoryProvider.overrideWithValue(repository)],
      child: const MaterialApp(
        home: Scaffold(
          body: SingleChildScrollView(
            child: CodeFlowCard(sessionId: 12, refreshKey: 'completed'),
          ),
        ),
      ),
    ),
  );
  await tester.pumpAndSettle();
}

void main() {
  test('mobile deploy entries use registered app-deploy routes', () {
    expect(websiteDeployTriggerPath, '/api/website/app-deploy/trigger');
    expect(websiteDeployListPath, '/api/website/app-deploy/list');
  });

  test('mobile sessions are isolated so they can be delivered', () {
    final body = codeSessionCreateBody(
      projectId: 3,
      executorId: 'codex',
      approvalPolicy: 'safe_auto',
    );
    expect(body['isolated'], isTrue);
  });

  test('delivery carries the reviewed revision the server requires', () {
    final review = CodeDeliveryReview.fromJson({
      'reviewReady': true,
      'reviewRevision': 'abc123',
      'files': 3,
      'additions': 12,
      'deletions': 4,
    });
    expect(review.ready, isTrue);
    expect(review.files, 3);
    expect(codeDeliveryMergeBody(review.revision), {
      'confirm': true,
      'reviewRevision': 'abc123',
    });
  });

  test('session flow hides when the project has nothing to show', () {
    final none = CodeSessionFlow.fromJson({'available': false});
    final idle = CodeSessionFlow.fromJson({
      'available': true,
      'canStart': false,
    });
    final startable = CodeSessionFlow.fromJson({
      'available': true,
      'canStart': true,
    });
    final withRun = CodeSessionFlow.fromJson({
      'available': true,
      'run': _waitingRun(),
    });
    expect(none.visible, isFalse);
    expect(idle.visible, isFalse);
    expect(startable.visible, isTrue);
    expect(withRun.run!.environments.last.isProduction, isTrue);
  });

  testWidgets('releases a waiting preview to production after confirmation', (
    tester,
  ) async {
    final repository = _FakeFlowRepository(
      CodeSessionFlow.fromJson({
        'available': true,
        'flowName': 'Shop Delivery',
        'run': _waitingRun(),
      }),
    );
    await _pumpCard(tester, repository);

    expect(find.text('Awaiting release'), findsOneWidget);
    expect(find.text('Open preview'), findsOneWidget);
    await tester.tap(find.widgetWithText(FilledButton, 'Release'));
    await tester.pumpAndSettle();
    expect(find.textContaining('This switches live traffic'), findsOneWidget);
    await tester.tap(find.widgetWithText(FilledButton, 'Deploy'));
    await tester.pumpAndSettle();

    expect(repository.deployed, [(41, 6)]);
  });

  testWidgets('offers a manual preview when auto start is off', (tester) async {
    final repository = _FakeFlowRepository(
      CodeSessionFlow.fromJson({
        'available': true,
        'flowName': 'Shop Delivery',
        'canStart': true,
      }),
    );
    await _pumpCard(tester, repository);

    await tester.tap(find.text('Generate preview'));
    await tester.pumpAndSettle();
    expect(repository.previewStarts, 1);
  });

  testWidgets('explains why an old release cannot be deployed', (tester) async {
    final run = _waitingRun()
      ..['deployable'] = false
      ..['deployBlocked'] = 'runner_target_unavailable';
    final repository = _FakeFlowRepository(
      CodeSessionFlow.fromJson({'available': true, 'run': run}),
    );
    await _pumpCard(tester, repository);

    expect(
      find.textContaining('Runner container was cleaned up'),
      findsOneWidget,
    );
    final release = tester.widget<FilledButton>(
      find.widgetWithText(FilledButton, 'Release'),
    );
    expect(release.onPressed, isNull);
  });

  testWidgets('shows auto preview in progress and picks up the run', (
    tester,
  ) async {
    final repository = _FakeFlowRepository(
      CodeSessionFlow.fromJson({
        'available': true,
        'flowName': 'Shop Delivery',
        'autoStart': true,
        'canStart': true,
        'autoStartPending': true,
      }),
    );
    tester.binding.platformDispatcher.localeTestValue = const Locale('en');
    addTearDown(tester.binding.platformDispatcher.clearLocaleTestValue);
    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          aiWorkspaceRepositoryProvider.overrideWithValue(repository),
        ],
        child: const MaterialApp(
          home: Scaffold(
            body: SingleChildScrollView(
              child: CodeFlowCard(sessionId: 12, refreshKey: 'completed'),
            ),
          ),
        ),
      ),
    );
    await tester.pump();

    expect(find.textContaining('Generating the preview'), findsOneWidget);
    expect(find.text('Generate preview'), findsNothing);

    repository.flow = CodeSessionFlow.fromJson({
      'available': true,
      'flowName': 'Shop Delivery',
      'run': _waitingRun(),
    });
    await tester.pump(const Duration(seconds: 5));
    await tester.pump();
    expect(find.text('Open preview'), findsOneWidget);
    expect(find.textContaining('Generating the preview'), findsNothing);
  });

  testWidgets('reloads when starting a preview fails', (tester) async {
    final repository = _FakeFlowRepository(
      CodeSessionFlow.fromJson({
        'available': true,
        'flowName': 'Shop Delivery',
        'canStart': true,
      }),
    )..startError = Exception('already started');
    await _pumpCard(tester, repository);
    final loadsBefore = repository.loads;
    repository.flow = CodeSessionFlow.fromJson({
      'available': true,
      'flowName': 'Shop Delivery',
      'run': _waitingRun(),
    });

    await tester.tap(find.text('Generate preview'));
    await tester.pumpAndSettle();
    expect(repository.loads, greaterThan(loadsBefore));
    expect(find.text('Generate preview'), findsNothing);
    expect(find.text('Open preview'), findsOneWidget);
  });
}
