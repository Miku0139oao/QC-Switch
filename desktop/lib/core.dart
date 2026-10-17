import 'dart:async';
import 'dart:convert';
import 'dart:io';

typedef Json = Map<String, dynamic>;
Json object(dynamic value) => Map<String, dynamic>.from(value as Map? ?? {});
List<Json> objects(dynamic value) =>
    (value as List? ?? []).map(object).toList();

class RpcError implements Exception {
  RpcError(this.message, {this.code = -1, this.data});
  final String message;
  final int code;
  final Json? data;
  @override
  String toString() => message;
}

abstract interface class RpcClient {
  Stream<void> get changes;
  Future<dynamic> call(String method, [Json? params]);
}

/// The management channel is newline-framed JSON-RPC over anonymous pipes.
/// It never opens an HTTP server, including when testing an upstream endpoint.
class CoreClient implements RpcClient {
  Process? _process;
  final _pending = <int, Completer<dynamic>>{};
  final _changes = StreamController<void>.broadcast();
  int _nextId = 0;
  bool _closing = false;
  String? failure;
  @override
  Stream<void> get changes => _changes.stream;

  Future<void> start() async {
    final root = File(Platform.resolvedExecutable).parent.path;
    final candidates = [
      if (Platform.environment['QCSWITCH_CORE'] case final String p) p,
      '$root/qc-switch-core.exe',
      '${Directory.current.path}/build/qc-switch-core.exe',
      '${Directory.current.parent.path}/build/qc-switch-core.exe',
    ];
    final path = candidates.where((p) => File(p).existsSync()).firstOrNull;
    if (path == null) {
      throw RpcError('找不到 qc-switch-core.exe。請完整解壓 portable ZIP，或先執行 Go 建置。');
    }
    final data = '${File(path).parent.path}/user-data';
    _process = await Process.start(path, ['--data-dir', data]);
    _process!.stdout
        .transform(utf8.decoder)
        .transform(const LineSplitter())
        .listen(_receive, onError: (Object e) => _fail('核心通訊中斷'));
    // Drain stderr, but never expose arbitrary logs or secrets in the UI.
    _process!.stderr.listen((_) {});
    unawaited(
      _process!.exitCode.then((code) {
        if (!_closing) _fail('核心已停止（代碼 $code）。重新啟動程式可檢查並恢復設定。');
      }),
    );
  }

  void _receive(String line) {
    try {
      final message = object(jsonDecode(line));
      if (message['method'] == 'state.changed') {
        _changes.add(null);
        return;
      }
      final pending = _pending.remove(message['id']);
      if (pending == null) return;
      if (message['error'] case final Map error) {
        pending.completeError(
          RpcError(
            error['message'] as String? ?? '核心操作失敗',
            code: error['code'] as int? ?? -1,
            data: error['data'] == null ? null : object(error['data']),
          ),
        );
      } else {
        pending.complete(message['result']);
      }
    } catch (_) {
      _fail('核心傳回了無效的通訊資料');
    }
  }

  void _fail(String message) {
    failure = message;
    for (final c in _pending.values) {
      c.completeError(RpcError(message));
    }
    _pending.clear();
    _changes.add(null);
  }

  @override
  Future<dynamic> call(String method, [Json? params]) async {
    if (failure != null) throw RpcError(failure!);
    if (_process == null) throw RpcError('核心尚未啟動');
    final id = ++_nextId;
    final result = Completer<dynamic>();
    _pending[id] = result;
    if (method == 'shutdown') _closing = true;
    try {
      _process!.stdin.writeln(
        jsonEncode({
          'jsonrpc': '2.0',
          'id': id,
          'method': method,
          'params': params ?? {},
        }),
      );
      await _process!.stdin.flush();
      return await result.future.timeout(const Duration(seconds: 155));
    } catch (_) {
      if (method == 'shutdown') _closing = false;
      rethrow;
    } finally {
      _pending.remove(id);
    }
  }
}
