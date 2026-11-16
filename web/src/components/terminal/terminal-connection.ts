interface TerminalConnectionOptions {
  directory: string;
  api: {
    createPTY(directory: string): Promise<{ id: string }>;
    deletePTY(id: string, directory: string): Promise<void>;
    resizePTY(id: string, cols: number, rows: number, directory: string): Promise<void>;
  };
  connect(id: string, directory: string): WebSocket;
  size(): { cols: number; rows: number };
  write(data: string | ArrayBuffer): void;
  ready(): void;
  error(reason: "error" | "disconnected"): void;
  disposeView(): void;
}

// Install ownership synchronously, before the asynchronous PTY allocation completes.
export function startTerminalConnection(options: TerminalConnectionOptions) {
  let disposed = false;
  let ptyID: string | undefined;
  let socket: WebSocket | undefined;
  const removePTY = (id: string) => {
    void options.api.deletePTY(id, options.directory).catch(() => undefined);
  };
  const dispose = () => {
    if (disposed) return;
    disposed = true;
    if (socket) {
      socket.onopen = socket.onmessage = socket.onerror = socket.onclose = null;
      try { socket.close(); } catch { /* already closed */ }
    }
    if (ptyID) removePTY(ptyID);
    options.disposeView();
  };
  const fail = (reason: "error" | "disconnected") => {
    if (disposed) return;
    options.error(reason);
    dispose();
  };
  const resize = () => {
    if (disposed || socket?.readyState !== 1 || !ptyID) return;
    const { cols, rows } = options.size();
    if (cols && rows) {
      void options.api.resizePTY(ptyID, cols, rows, options.directory).catch(() => undefined);
    }
  };

  void (async () => {
    try {
      const pty = await options.api.createPTY(options.directory);
      if (disposed) {
        removePTY(pty.id);
        return;
      }
      ptyID = pty.id;
      socket = options.connect(ptyID, options.directory);
      socket.binaryType = "arraybuffer";
      socket.onopen = () => {
        if (disposed) return;
        options.ready();
        resize();
      };
      socket.onmessage = (event) => {
        if (!disposed) options.write(event.data);
      };
      socket.onerror = () => fail("error");
      socket.onclose = () => fail("disconnected");
    } catch {
      fail("error");
    }
  })();

  return {
    dispose,
    resize,
    send(chunk: string) {
      if (!disposed && socket?.readyState === 1) socket.send(chunk);
    },
  };
}
