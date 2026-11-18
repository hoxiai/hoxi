import 'package:flutter_riverpod/flutter_riverpod.dart';

export 'navigation_guard.dart';

const String chatConversationRoutePath = '/chat';
const String modelGatewayRoutePath = '/model_gateway';

final mainScaffoldIndexProvider =
    NotifierProvider<MainScaffoldIndexController, int>(
      MainScaffoldIndexController.new,
    );

final engineeringSectionProvider =
    NotifierProvider<EngineeringSectionController, int>(
      EngineeringSectionController.new,
    );

final pendingChatPromptProvider =
    NotifierProvider<PendingChatPromptController, String?>(
      PendingChatPromptController.new,
    );

final nodeDisplayNicknameProvider =
    NotifierProvider<NodeDisplayNicknameController, String>(
      NodeDisplayNicknameController.new,
    );

class PendingChatPromptController extends Notifier<String?> {
  @override
  String? build() => null;

  void requestPrompt(String prompt) {
    final trimmed = prompt.trim();
    if (trimmed.isEmpty) return;
    state = trimmed;
  }

  String? takePrompt() {
    final current = state;
    state = null;
    return current;
  }
}

class NodeDisplayNicknameController extends Notifier<String> {
  @override
  String build() => '';

  void setNickname(String nickname) {
    state = nickname.trim();
  }
}

class EngineeringSectionController extends Notifier<int> {
  static const int codeSegment = 0;
  static const int monitorSegment = 1;
  static const int resourcesSegment = 2;
  static const int managementSegment = 3;

  @override
  int build() => codeSegment;

  void setSegment(int segment) {
    state = segment;
  }
}

class MainScaffoldIndexController extends Notifier<int> {
  static const int chatIndex = 0;
  static const int engineeringIndex = 1;
  static const int columnIndex = 2;
  static const int settingsIndex = 3;

  // 兼容原有页面跳转到「工作台」对应子分段的常量
  static const int overviewIndex = engineeringIndex;
  static const int codeIndex = engineeringIndex;
  static const int resourcesIndex = engineeringIndex;

  @override
  int build() => chatIndex;

  void setIndex(int index) {
    state = index;
  }

  void openEngineeringSegment(int segment) {
    ref.read(engineeringSectionProvider.notifier).setSegment(segment);
    state = engineeringIndex;
  }
}
