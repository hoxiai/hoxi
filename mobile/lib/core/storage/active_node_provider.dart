import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'storage_service.dart';

final activeNodeGenerationProvider =
    NotifierProvider<ActiveNodeGeneration, int>(ActiveNodeGeneration.new);

class ActiveNodeGeneration extends Notifier<int> {
  @override
  int build() {
    void onChanged() => state = StorageService.nodeGeneration.value;
    StorageService.nodeGeneration.addListener(onChanged);
    ref.onDispose(
      () => StorageService.nodeGeneration.removeListener(onChanged),
    );
    return StorageService.nodeGeneration.value;
  }
}
