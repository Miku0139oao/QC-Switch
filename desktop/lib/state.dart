import 'dart:async';

import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'core.dart';

final coreProvider = Provider<RpcClient>((ref) => throw StateError('No core'));
final appStateProvider = AsyncNotifierProvider<AppController, Json>(
  AppController.new,
);

class AppController extends AsyncNotifier<Json> {
  Timer? _debounce;
  bool _refreshing = false;
  @override
  Future<Json> build() async {
    final core = ref.watch(coreProvider);
    final subscription = core.changes.listen((_) {
      _debounce ??= Timer(const Duration(milliseconds: 350), () {
        _debounce = null;
        unawaited(refresh());
      });
    });
    ref.onDispose(() {
      subscription.cancel();
      _debounce?.cancel();
    });
    final result = object(await core.call('initialize'));
    if (result['contract'] != 1) {
      throw RpcError('核心版本不相容，請使用同一份 portable ZIP 中的程式。');
    }
    return result;
  }

  Future<void> refresh() async {
    if (_refreshing) return;
    _refreshing = true;
    try {
      state = AsyncData(object(await ref.read(coreProvider).call('state.get')));
    } catch (e, stack) {
      state = AsyncError(e, stack);
    } finally {
      _refreshing = false;
    }
  }

  Future<dynamic> invoke(String method, [Json? params]) async {
    final value = await ref.read(coreProvider).call(method, params);
    if (value is Map && value.containsKey('contract')) {
      state = AsyncData(object(value));
    }
    return value;
  }
}
