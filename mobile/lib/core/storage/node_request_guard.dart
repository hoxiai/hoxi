import 'active_node_context.dart';
import 'storage_service.dart';

/// Protect state updates even when a repository is replaced before its old
/// Future completes, or the connection changes before Riverpod rebuilds it.
class NodeRequestGuard<T> {
  final T repository;
  final ActiveNodeContext node = StorageService.activeNodeContext;

  NodeRequestGuard(this.repository);

  bool isCurrent(T currentRepository) =>
      identical(repository, currentRepository) &&
      node.generation == StorageService.nodeGeneration.value;
}
