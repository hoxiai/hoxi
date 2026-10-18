import { onCleanup, onMount } from "solid-js";

const WIDTH = 640;
const HEIGHT = 240;
const COUNT = 1024;
const ASSEMBLE_MS = 2800;
const FLIGHT_MS = 650;

interface Particle {
  tx: number;
  ty: number;
  x: number;
  y: number;
  sx: number;
  sy: number;
  vx: number;
  vy: number;
  delay: number;
}

export default function ParticleLogo() {
  let canvas!: HTMLCanvasElement;

  onMount(() => {
    const context = canvas.getContext("2d");
    if (!context) return;

    const mask = document.createElement("canvas");
    mask.width = WIDTH;
    mask.height = HEIGHT;
    const ink = mask.getContext("2d", { willReadFrequently: true });
    if (!ink) return;
    // Use local fonts so the empty state also works offline in the desktop app.
    ink.font = '900 180px "Inter", -apple-system, BlinkMacSystemFont, sans-serif';
    ink.textAlign = "center";
    const metrics = ink.measureText("Hoxi");
    ink.fillText("Hoxi", WIDTH / 2, (HEIGHT + metrics.actualBoundingBoxAscent - metrics.actualBoundingBoxDescent) / 2);
    const pixels = ink.getImageData(0, 0, WIDTH, HEIGHT).data;
    let points: { x: number; y: number }[] = [];
    let gridStep = 5;
    // Keep whole grid cells; thinning individual points leaves uneven holes.
    for (let step = 3; step <= 9; step += 0.125) {
      const candidate: { x: number; y: number }[] = [];
      const columns = Math.floor(WIDTH / step);
      const rows = Math.floor(HEIGHT / step);
      const offsetX = (WIDTH - columns * step) / 2;
      const offsetY = (HEIGHT - rows * step) / 2;
      for (let row = 0; row < rows; row++) {
        const y = offsetY + row * step;
        for (let column = 0; column < columns; column++) {
          const x = offsetX + column * step;
          if (pixels[(Math.round(y) * WIDTH + Math.round(x)) * 4 + 3] > 100) {
            candidate.push({ x, y });
          }
        }
      }
      if (candidate.length && Math.abs(candidate.length - COUNT) < Math.abs(points.length - COUNT)) {
        points = candidate;
        gridStep = step;
      }
    }
    if (!points.length) return;

    const minX = Math.min(...points.map((point) => point.x));
    const maxX = Math.max(...points.map((point) => point.x));
    const radius = gridStep * 0.3;
    const particles: Particle[] = points.map((point) => {
      const sx = WIDTH + 50 + Math.random() * 160;
      const sy = point.y + (Math.random() - 0.5) * 80;
      return {
        tx: point.x, ty: point.y, x: sx, y: sy, sx, sy, vx: 0, vy: 0,
        delay: (point.x - minX) / (maxX - minX || 1) * (ASSEMBLE_MS - FLIGHT_MS) + point.y / HEIGHT * 22,
      };
    });

    const reducedMotion = window.matchMedia("(prefers-reduced-motion: reduce)");
    let frame = 0;
    let startedAt = performance.now();
    let previousTime = startedAt;
    let assembled = reducedMotion.matches;
    let visible = true;
    let disposed = false;
    let mouseX = -9999;
    let mouseY = -9999;
    let scale = 1;

    const draw = () => {
      context.clearRect(0, 0, WIDTH, HEIGHT);
      context.fillStyle = "#2563eb";
      context.beginPath();
      for (const particle of particles) {
        if (!assembled && previousTime - startedAt < particle.delay) continue;
        context.moveTo(particle.x + radius, particle.y);
        context.arc(particle.x, particle.y, radius, 0, Math.PI * 2);
      }
      context.fill();
    };

    const settle = () => {
      assembled = true;
      for (const particle of particles) {
        particle.x = particle.tx;
        particle.y = particle.ty;
        particle.vx = particle.vy = 0;
      }
      draw();
    };

    const render = (now: number) => {
      frame = 0;
      if (disposed || !visible || document.hidden) return;
      const elapsed = now - startedAt;
      // Normalize the spring simulation across 60 Hz and high-refresh displays.
      const step = Math.min(2, Math.max(0.1, (now - previousTime) / (1000 / 60)));
      previousTime = now;
      let moving = false;
      for (const particle of particles) {
        if (!assembled && elapsed < particle.delay + FLIGHT_MS) {
          moving = true;
          if (elapsed < particle.delay) continue;
          const progress = Math.min(1, (elapsed - particle.delay) / FLIGHT_MS);
          const eased = 1 - (1 - progress) ** 3;
          particle.x = particle.sx + (particle.tx - particle.sx) * eased;
          particle.y = particle.sy + (particle.ty - particle.sy) * eased;
          continue;
        }
        const dx = particle.tx - particle.x;
        const dy = particle.ty - particle.y;
        particle.vx += dx * 0.065 * step;
        particle.vy += dy * 0.065 * step;
        const mx = particle.x - mouseX;
        const my = particle.y - mouseY;
        const distance = Math.hypot(mx, my);
        if (distance < 85 && distance > 0.001) {
          const force = (1 - distance / 85) * 8.5 * step;
          particle.vx += mx / distance * force;
          particle.vy += my / distance * force;
        }
        particle.vx *= 0.84 ** step;
        particle.vy *= 0.84 ** step;
        particle.x += particle.vx * step;
        particle.y += particle.vy * step;
        if (Math.abs(dx) + Math.abs(dy) + Math.abs(particle.vx) + Math.abs(particle.vy) > 0.08) {
          moving = true;
        } else {
          particle.x = particle.tx;
          particle.y = particle.ty;
          particle.vx = particle.vy = 0;
        }
      }
      if (elapsed >= ASSEMBLE_MS + 22) assembled = true;
      draw();
      if (moving) wake();
    };

    function wake() {
      if (!frame && !disposed && visible && !document.hidden && !reducedMotion.matches) {
        frame = requestAnimationFrame(render);
      }
    }

    const resize = () => {
      const bounds = canvas.getBoundingClientRect();
      if (!bounds.width) return;
      scale = bounds.width / WIDTH;
      const dpr = window.devicePixelRatio || 1;
      canvas.width = Math.round(bounds.width * dpr);
      canvas.height = Math.round(bounds.height * dpr);
      context.setTransform(canvas.width / WIDTH, 0, 0, canvas.height / HEIGHT, 0, 0);
      draw();
    };
    const move = (event: PointerEvent) => {
      if (reducedMotion.matches) return;
      const bounds = canvas.getBoundingClientRect();
      mouseX = (event.clientX - bounds.left) / scale;
      mouseY = (event.clientY - bounds.top) / scale;
      previousTime = performance.now();
      wake();
    };
    const leave = () => {
      mouseX = mouseY = -9999;
      wake();
    };
    const resume = () => {
      previousTime = performance.now();
      wake();
    };
    const motionChanged = () => {
      if (reducedMotion.matches) {
        cancelAnimationFrame(frame);
        frame = 0;
        settle();
      }
    };
    const resizeObserver = new ResizeObserver(resize);
    const visibilityObserver = new IntersectionObserver((entries) => {
      visible = entries[0]?.isIntersecting ?? false;
      if (visible) resume();
    });
    resizeObserver.observe(canvas);
    visibilityObserver.observe(canvas);
    canvas.addEventListener("pointermove", move);
    canvas.addEventListener("pointerleave", leave);
    canvas.addEventListener("pointerup", leave);
    canvas.addEventListener("pointercancel", leave);
    document.addEventListener("visibilitychange", resume);
    reducedMotion.addEventListener("change", motionChanged);
    resize();
    if (reducedMotion.matches) settle();
    else {
      startedAt = previousTime = performance.now();
      wake();
    }

    onCleanup(() => {
      disposed = true;
      cancelAnimationFrame(frame);
      resizeObserver.disconnect();
      visibilityObserver.disconnect();
      canvas.removeEventListener("pointermove", move);
      canvas.removeEventListener("pointerleave", leave);
      canvas.removeEventListener("pointerup", leave);
      canvas.removeEventListener("pointercancel", leave);
      document.removeEventListener("visibilitychange", resume);
      reducedMotion.removeEventListener("change", motionChanged);
    });
  });

  return (
    <div class="mx-auto mb-4 w-[min(100%,480px)] shrink-0 px-6" aria-hidden="true">
      <canvas ref={canvas} class="block aspect-[8/3] w-full" />
    </div>
  );
}
