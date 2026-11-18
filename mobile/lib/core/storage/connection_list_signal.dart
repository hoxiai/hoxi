import 'dart:async';

import 'package:flutter/foundation.dart';

/// Saved-list changes do not invalidate active-node requests or cookies.
final connectionListGeneration = ValueNotifier<int>(0);

/// Zone-safe FIFO write serializer that retains no completed [Future] across
/// idle boundaries (avoiding cross-Zone deadlocks between `Zone.root` and
/// `FakeAsync` in widget tests).
class SerializedWriteQueue {
  Completer<void>? _tail;

  Future<T> run<T>(Future<T> Function() action) async {
    final prev = _tail;
    final current = Completer<void>();
    _tail = current;
    if (prev != null) {
      await prev.future;
    } else {
      await Future<void>.value();
    }
    try {
      return await action();
    } finally {
      if (identical(_tail, current)) {
        _tail = null;
      }
      current.complete();
    }
  }

  void reset() {
    _tail = null;
  }
}

final SerializedWriteQueue _listWrites = SerializedWriteQueue();

/// Keep list snapshots and secure-storage updates in a single ordered mutation.
Future<T> serializeConnectionListWrite<T>(Future<T> Function() action) =>
    _listWrites.run(action);
