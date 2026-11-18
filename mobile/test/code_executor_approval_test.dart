import 'package:flutter_test/flutter_test.dart';
import 'package:hoxi/features/ai_workspace/models/ai_dev_session.dart';

CodeExecutor _executor(List<String> capabilities, List<String> policies) {
  return CodeExecutor.fromJson({
    'id': 'x',
    'name': 'X',
    'capabilities': capabilities,
    'approvalPolicies': policies,
  });
}

void main() {
  const all = ['manual', 'safe_auto', 'full_auto'];

  test('没有审批桥的执行器选手动确认/安全自动时提示会被直接拒绝', () {
    final claude = _executor(['structured_turn'], all);
    expect(claude.rejectsApprovalInConversation('manual'), isTrue);
    expect(claude.rejectsApprovalInConversation('safe_auto'), isTrue);
    expect(claude.rejectsApprovalInConversation('full_auto'), isFalse);
  });

  test('有审批桥、只有一档或没有对话模式时不提示', () {
    final codex = _executor(['structured_turn', 'conversation_approval'], all);
    expect(codex.rejectsApprovalInConversation('manual'), isFalse);
    expect(
      _executor(
        ['structured_turn'],
        ['full_auto'],
      ).rejectsApprovalInConversation('safe_auto'),
      isFalse,
    );
    expect(_executor([], all).rejectsApprovalInConversation('manual'), isFalse);
    expect(
      _executor(['structured_turn'], all).rejectsApprovalInConversation(null),
      isFalse,
    );
  });
}
