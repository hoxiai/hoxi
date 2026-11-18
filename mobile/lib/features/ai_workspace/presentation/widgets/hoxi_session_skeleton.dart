import 'dart:math' as math;

import 'package:flutter/material.dart';
import 'package:flutter/scheduler.dart';

import '../../../../shared/widgets/hoxi_wordmark_grid.dart';
import '../../../../core/theme/app_theme.dart';

const List<double> _kLoadingAlphaBands = <double>[
  0.06,
  0.09,
  0.13,
  0.17,
  0.22,
  0.28,
];

/// 1:1 port of PC desktop `sampleLoadingShimmer` in
/// `web/src/components/timeline/ParticleLogo.tsx`.
double sampleHoxiLoadingShimmer(double u, double v, double elapsedSec) {
  final sweep = ((elapsedSec * 0.65) % 1.6) - 0.25;
  final proj = u + (v - 0.5) * 0.18;
  final dist = (proj - sweep).abs();
  final rawBand = math.max(0.0, 1.0 - dist / 0.28);
  final smoothBand = rawBand * rawBand * (3.0 - 2.0 * rawBand);
  final ripple =
      0.5 + 0.5 * math.sin(u * 8.5 + v * 4.5 - elapsedSec * 3.0);
  return math.min(1.0, smoothBand * 0.82 + ripple * 0.18);
}

/// 1:1 port of PC desktop `SessionSkeleton.tsx` (`<ParticleLogo loading />`):
/// shown centered while a session's messages or task logs are being fetched for
/// the first time, rendering the 1037-point Hoxi particle wordmark with a
/// gentle wave shimmer and `hoxi-fade-enter` (`240ms`).
class HoxiSessionSkeleton extends StatefulWidget {
  const HoxiSessionSkeleton({
    super.key,
    this.maxWidth = 300,
  });

  final double maxWidth;

  @override
  State<HoxiSessionSkeleton> createState() => _HoxiSessionSkeletonState();
}

class _HoxiSessionSkeletonState extends State<HoxiSessionSkeleton>
    with SingleTickerProviderStateMixin {
  static final List<_ShimmerParticle> _particles = _buildShimmerParticles();

  Ticker? _ticker;
  double _elapsedSec = 0.0;
  double _fadeIn = 0.0;

  @override
  void initState() {
    super.initState();
    _ticker = createTicker(_onTick);
  }

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    final disableAnimations = MediaQuery.maybeDisableAnimationsOf(context) ?? false;
    if (disableAnimations) {
      _ticker?.stop();
      _fadeIn = 1.0;
    } else if (!(_ticker?.isActive ?? false)) {
      _ticker?.start();
    }
  }

  void _onTick(Duration elapsed) {
    if (!mounted) return;
    final sec = elapsed.inMicroseconds / 1000000.0;
    // Matches `.hoxi-fade-enter` (`240ms`, `cubic-bezier(0.16, 1, 0.3, 1)`).
    final fadeProgress = (sec / 0.24).clamp(0.0, 1.0);
    final easedFade = const Cubic(0.16, 1.0, 0.3, 1.0).transform(fadeProgress);
    setState(() {
      _elapsedSec = sec;
      _fadeIn = easedFade;
    });
  }

  @override
  void dispose() {
    _ticker?.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final reducedMotion = MediaQuery.maybeDisableAnimationsOf(context) ?? false;
    final accentColor = AppTheme.isDark
        ? const Color(0xFF3B82F6)
        : AppTheme.primaryBlue;

    return Semantics(
      container: true,
      liveRegion: true,
      label: 'Loading session',
      child: Center(
        child: Padding(
          padding: const EdgeInsets.symmetric(horizontal: 32, vertical: 24),
          child: Opacity(
            opacity: reducedMotion ? 1.0 : _fadeIn,
            child: ConstrainedBox(
              constraints: BoxConstraints(maxWidth: widget.maxWidth),
              child: AspectRatio(
                aspectRatio:
                    kHoxiWordmarkCanvasWidth / kHoxiWordmarkCanvasHeight,
                child: RepaintBoundary(
                  child: CustomPaint(
                    painter: _HoxiLoadingParticlePainter(
                      particles: _particles,
                      elapsedSec: _elapsedSec,
                      accentColor: accentColor,
                      reducedMotion: reducedMotion,
                    ),
                  ),
                ),
              ),
            ),
          ),
        ),
      ),
    );
  }
}

class _ShimmerParticle {
  final double x;
  final double y;
  final double u;
  final double v;

  const _ShimmerParticle({
    required this.x,
    required this.y,
    required this.u,
    required this.v,
  });
}

List<_ShimmerParticle> _buildShimmerParticles() {
  final columns = (kHoxiWordmarkCanvasWidth / kHoxiWordmarkGridStep).floor();
  final rows = (kHoxiWordmarkCanvasHeight / kHoxiWordmarkGridStep).floor();
  final offsetX =
      (kHoxiWordmarkCanvasWidth - columns * kHoxiWordmarkGridStep) / 2.0;
  final offsetY =
      (kHoxiWordmarkCanvasHeight - rows * kHoxiWordmarkGridStep) / 2.0;
  final spanX = kHoxiWordmarkMaxX - kHoxiWordmarkMinX;

  return kHoxiWordmarkPackedCells.map((packed) {
    final col = (packed >> 8) & 0xFF;
    final row = packed & 0xFF;
    final x = offsetX + col * kHoxiWordmarkGridStep;
    final y = offsetY + row * kHoxiWordmarkGridStep;
    return _ShimmerParticle(
      x: x,
      y: y,
      u: ((x - kHoxiWordmarkMinX) / spanX).clamp(0.0, 1.0),
      v: (y / kHoxiWordmarkCanvasHeight).clamp(0.0, 1.0),
    );
  }).toList(growable: false);
}

class _HoxiLoadingParticlePainter extends CustomPainter {
  const _HoxiLoadingParticlePainter({
    required this.particles,
    required this.elapsedSec,
    required this.accentColor,
    required this.reducedMotion,
  });

  final List<_ShimmerParticle> particles;
  final double elapsedSec;
  final Color accentColor;
  final bool reducedMotion;

  @override
  void paint(Canvas canvas, Size size) {
    if (size.width <= 0 || size.height <= 0) return;
    final scale = size.width / kHoxiWordmarkCanvasWidth;
    canvas.save();
    canvas.scale(scale, scale);

    const baseRadius = kHoxiWordmarkGridStep * 0.30;
    final paint = Paint()..style = PaintingStyle.fill;

    if (reducedMotion) {
      paint.color = accentColor.withValues(alpha: 0.12);
      for (final p in particles) {
        canvas.drawCircle(Offset(p.x, p.y), baseRadius, paint);
      }
      canvas.restore();
      return;
    }

    final bandCount = _kLoadingAlphaBands.length;
    final buckets = List<List<(_ShimmerParticle, double)>>.generate(
      bandCount,
      (_) => <(_ShimmerParticle, double)>[],
      growable: false,
    );

    for (final p in particles) {
      final crest = sampleHoxiLoadingShimmer(p.u, p.v, elapsedSec);
      final idx = (crest * bandCount).floor().clamp(0, bandCount - 1);
      buckets[idx].add((p, crest));
    }

    for (var i = 0; i < bandCount; i++) {
      final bucket = buckets[i];
      if (bucket.isEmpty) continue;
      paint.color = accentColor.withValues(alpha: _kLoadingAlphaBands[i]);
      for (final (p, crest) in bucket) {
        final r = baseRadius * (0.78 + 0.34 * crest);
        canvas.drawCircle(Offset(p.x, p.y), r, paint);
      }
    }

    canvas.restore();
  }

  @override
  bool shouldRepaint(covariant _HoxiLoadingParticlePainter oldDelegate) {
    return oldDelegate.elapsedSec != elapsedSec ||
        oldDelegate.accentColor != accentColor ||
        oldDelegate.reducedMotion != reducedMotion;
  }
}

/// 1:1 port of PC desktop `.hoxi-view-enter` (`web/src/transitions.css` &
/// `styles.css`):
/// - `--motion-enter: 260ms`
/// - `--ease-enter: cubic-bezier(0.16, 1, 0.3, 1)`
/// - `translate3d(0, 8px, 0) scale(0.994)` -> `none`, `opacity: 0` -> `1`
///
/// When [deferOneFrame] is true, holds `opacity: 0` during the initial layout
/// frame so bottom scroll positioning (`jumpTo(maxScrollExtent)`) settles
/// invisibly before revealing the view—eliminating 1-frame scroll-jump flashes.
class HoxiViewEntrance extends StatefulWidget {
  const HoxiViewEntrance({
    super.key,
    required this.child,
    this.identity,
    this.deferOneFrame = true,
    this.duration = const Duration(milliseconds: 260),
  });

  final Widget child;
  final Object? identity;
  final bool deferOneFrame;
  final Duration duration;

  @override
  State<HoxiViewEntrance> createState() => _HoxiViewEntranceState();
}

class _HoxiViewEntranceState extends State<HoxiViewEntrance>
    with SingleTickerProviderStateMixin {
  static const Cubic _easeEnter = Cubic(0.16, 1.0, 0.3, 1.0);
  static const Cubic _easeMove = Cubic(0.22, 0.61, 0.36, 1.0);

  late final AnimationController _controller;
  int _playGeneration = 0;

  @override
  void initState() {
    super.initState();
    _controller = AnimationController(vsync: this, duration: widget.duration);
    _scheduleEntrance();
  }

  @override
  void didUpdateWidget(covariant HoxiViewEntrance oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.duration != widget.duration) {
      _controller.duration = widget.duration;
    }
    if (oldWidget.identity != widget.identity) {
      _scheduleEntrance();
    }
  }

  void _scheduleEntrance() {
    final gen = ++_playGeneration;
    _controller.value = 0.0;
    if (!widget.deferOneFrame) {
      _startForward(gen);
      return;
    }
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted || gen != _playGeneration) return;
      _startForward(gen);
    });
  }

  void _startForward(int gen) {
    if (!mounted || gen != _playGeneration) return;
    if (MediaQuery.maybeDisableAnimationsOf(context) ?? false) {
      _controller.value = 1.0;
      return;
    }
    _controller.forward(from: 0.0);
  }

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    if (MediaQuery.maybeDisableAnimationsOf(context) ?? false) {
      return widget.child;
    }
    return AnimatedBuilder(
      animation: _controller,
      child: widget.child,
      builder: (context, child) {
        final t = _controller.value;
        if (t >= 0.999) return child!;
        final opacity = _easeEnter.transform(t).clamp(0.0, 1.0);
        final move = _easeMove.transform(t);
        final dy = 8.0 * (1.0 - move);
        final scale = 0.994 + 0.006 * move;
        return Opacity(
          opacity: opacity,
          child: Transform.translate(
            offset: Offset(0, dy),
            child: Transform.scale(
              scale: scale,
              child: child,
            ),
          ),
        );
      },
    );
  }
}
