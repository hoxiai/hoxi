import { playNotificationSound } from "../store/settings";
import { assertEqual, describe, it } from "./test-harness";

class FakeParam {
  setValueAtTime() {}
  exponentialRampToValueAtTime() {}
}

class FakeNode {
  disconnected = false;
  connect() {}
  disconnect() {
    this.disconnected = true;
  }
}

class FakeOscillator extends FakeNode {
  type = "";
  frequency = new FakeParam();
  onended: (() => void) | null = null;
  start() {}
  stop() {}
}

class FakeGain extends FakeNode {
  gain = new FakeParam();
}

class FakeAudioContext {
  static created: FakeAudioContext[] = [];
  state: "running" | "suspended" = "running";
  currentTime = 0;
  destination = new FakeNode();
  oscillators: FakeOscillator[] = [];
  gains: FakeGain[] = [];
  suspendCalls = 0;
  resumeCalls = 0;

  constructor() {
    FakeAudioContext.created.push(this);
  }
  createOscillator() {
    const node = new FakeOscillator();
    this.oscillators.push(node);
    return node;
  }
  createGain() {
    const node = new FakeGain();
    this.gains.push(node);
    return node;
  }
  suspend() {
    this.suspendCalls++;
    this.state = "suspended";
    return Promise.resolve();
  }
  resume() {
    this.resumeCalls++;
    this.state = "running";
    return Promise.resolve();
  }
}

describe("notification sound", () => {
  it("releases the audio output once the last chime ends and resumes for the next one", () => {
    const g = globalThis as unknown as { window?: unknown };
    const prevWindow = g.window;
    g.window = { AudioContext: FakeAudioContext };
    try {
      playNotificationSound(true);
      playNotificationSound(true);
      assertEqual(FakeAudioContext.created.length, 1, "one shared context");
      const ctx = FakeAudioContext.created[0];
      assertEqual(ctx.oscillators.length, 2, "two chimes scheduled");
      assertEqual(ctx.state, "running", "context runs while chimes play");

      ctx.oscillators[0].onended?.();
      assertEqual(ctx.suspendCalls, 0, "still playing the second chime");
      assertEqual(ctx.oscillators[0].disconnected && ctx.gains[0].disconnected, true, "finished chime is disconnected");

      ctx.oscillators[1].onended?.();
      assertEqual(ctx.suspendCalls, 1, "suspended after the last chime");
      assertEqual(ctx.state, "suspended", "output released");

      const resumesBefore = ctx.resumeCalls;
      playNotificationSound(true);
      assertEqual(ctx.resumeCalls, resumesBefore + 1, "next chime resumes the context");
      assertEqual(ctx.state, "running", "context runs again");
      ctx.oscillators[2].onended?.();
      assertEqual(ctx.suspendCalls, 2, "suspended again afterwards");
    } finally {
      if (prevWindow === undefined) {
        delete g.window;
      } else {
        g.window = prevWindow;
      }
    }
  });
});
