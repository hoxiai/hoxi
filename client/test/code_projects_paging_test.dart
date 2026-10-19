import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:gopanel/core/network/api_client.dart';
import 'package:gopanel/core/network/api_response.dart';
import 'package:gopanel/features/ai_workspace/data/ai_workspace_repository.dart';

class _PagedProjectsApiClient implements ApiClient {
  _PagedProjectsApiClient(this.total, {this.stopAfter});

  final int total;
  final int? stopAfter;
  final List<Map<String, dynamic>?> queries = [];

  @override
  Future<ApiResponse<T>> get<T>(
    String path, {
    Map<String, dynamic>? queryParameters,
    Options? options,
  }) async {
    queries.add(queryParameters);
    final page = queryParameters!['page'] as int;
    final limit = queryParameters['limit'] as int;
    final available = stopAfter ?? total;
    final start = (page - 1) * limit;
    final end = (start + limit).clamp(0, available);
    final items = [
      for (var id = start + 1; id <= end; id++) {'id': id, 'name': 'p$id'},
    ];
    return ApiResponse<T>(
      code: 0,
      msg: '',
      data: {'items': items, 'total': total} as T,
    );
  }

  @override
  dynamic noSuchMethod(Invocation invocation) => super.noSuchMethod(invocation);
}

void main() {
  test('getProjects pages through every project', () async {
    final client = _PagedProjectsApiClient(230);

    final projects = await AiWorkspaceRepository(client).getProjects();

    expect(projects, hasLength(230));
    expect(projects.last.id, 230);
    expect(client.queries.map((q) => q!['page']), [1, 2, 3]);
    expect(client.queries.every((q) => q!['limit'] == 100), isTrue);
  });

  test('getProjects stops on an empty page', () async {
    final client = _PagedProjectsApiClient(250, stopAfter: 100);

    final projects = await AiWorkspaceRepository(client).getProjects();

    expect(projects, hasLength(100));
    expect(client.queries, hasLength(2));
  });
}
