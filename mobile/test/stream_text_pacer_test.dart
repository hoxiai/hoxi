import 'package:flutter_test/flutter_test.dart';
import 'package:hoxi/shared/widgets/stream_text_pacer.dart';

void main() {
  test('StreamTextPacer 对齐 PC 端：原子跳过完整 Markdown 行内标记并按帧平滑吐字', () {
    final pacer = StreamTextPacer();
    const chunk = '先看 `main.dart` 和 **关键配置**，然后执行下一步。';
    expect(pacer.push(chunk, 0), isTrue);
    expect(pacer.pending, isTrue);
    expect(pacer.shown, greaterThan(0));
    expect(pacer.shown, lessThan(chunk.length));

    var t = 0.0;
    while (pacer.pending && t < 2000) {
      t += 16.67;
      final next = pacer.step(t);
      final slice = chunk.substring(0, next);
      expect(slice.endsWith('`main.da'), isFalse);
      expect(slice.endsWith('**关键配'), isFalse);
    }
    expect(pacer.displayedText, chunk);
    expect(
      extractLiveReasoningTicker(
        '### 1. 检查路由\n正在分析控制器\n**2. 验证流式吐字**\n细节行',
        isStreaming: true,
      ),
      '2. 验证流式吐字',
    );
  });
}
