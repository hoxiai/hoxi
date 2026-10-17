import { onCleanup, onMount } from "solid-js";
import ThreePaneShell from "./components/layout/ThreePaneShell";
import { hoxiAPI } from "./services/api";
import { connectEventStream } from "./services/sse";
import {
  applyPartDeltaBatch,
  loadAgents,
  loadProjects,
  loadProviders,
  loadProviderTelemetry,
  loadSessionMessages,
  recordSessionRetry,
  refreshWorktreeStatus,
  runSessionCodeAudit,
  selectSession,
  setWorkspace,
  upsertMessageInfo,
  upsertMessagePart,
} from "./store/workspace";

export default function App() {
  onMount(async () => {
    try {
      const boot = await hoxiAPI.getBootstrap();
      setWorkspace({
        rootDir: boot.rootDir,
        wsBaseURL: boot.wsBaseURL,
      });

      // Load OpenCode projects, providers, models, agents, telemetry & pending questions in parallel
      void loadProjects();
      void loadProviders();
      void loadProviderTelemetry();
      void loadAgents();
      void refreshWorktreeStatus("__root__");
      void hoxiAPI
        .listQuestions()
        .then((qs) => {
          if (Array.isArray(qs)) setWorkspace("pendingQuestions", qs);
        })
        .catch(() => {});

      const sessions = await hoxiAPI.listSessions();
      setWorkspace("sessions", sessions);

      // Restore each session's worktree & git branch/diff status
      await Promise.allSettled(sessions.map((session) => refreshWorktreeStatus(session.id)));

      if (sessions.length > 0) {
        const mostRecent = [...sessions].sort((a, b) => b.time.created - a.time.created)[0];
        await selectSession(mostRecent.id);
      }
    } catch (err) {
      console.error("Bootstrap error:", err);
    }

    const disconnect = connectEventStream({
      onConnectionChange: (connected) => {
        setWorkspace("connected", connected);
        if (connected) {
          void loadProviders();
          void loadProviderTelemetry();
        }
      },
      onDeltaBatch: applyPartDeltaBatch,
      onEvent: (evt) => {
        switch (evt.type) {
          case "message.updated":
            upsertMessageInfo(evt.properties.info);
            break;
          case "message.part.updated":
            upsertMessagePart(evt.properties.part);
            break;
          case "session.updated":
            replaceSession(evt.properties.info);
            break;
          case "session.status":
            setWorkspace("statusBySession", evt.properties.sessionID, evt.properties.status);
            if (evt.properties.status.type === "retry") {
              recordSessionRetry(
                evt.properties.sessionID,
                evt.properties.status.attempt,
                evt.properties.status.message
              );
            }
            break;
          case "session.idle":
            setWorkspace("statusBySession", evt.properties.sessionID, { type: "idle" });
            void loadSessionMessages(evt.properties.sessionID).then(() => {
              void runSessionCodeAudit(evt.properties.sessionID, false);
            });
            break;
          case "session.diff":
            setWorkspace("diffsBySession", evt.properties.sessionID, evt.properties.diff);
            void refreshWorktreeStatus(evt.properties.sessionID);
            break;
          case "permission.asked":
            setWorkspace("pendingPermissions", (prev) =>
              prev.some((p) => p.id === evt.properties.id) ? prev : [...prev, evt.properties]
            );
            break;
          case "permission.replied":
            setWorkspace("pendingPermissions", (prev) =>
              prev.filter((p) => p.id !== evt.properties.requestID)
            );
            break;
          case "question.asked":
          case "question.v2.asked":
            setWorkspace("pendingQuestions", (prev) =>
              prev.some((q) => q.id === evt.properties.id) ? prev : [...prev, evt.properties]
            );
            break;
          case "question.replied":
          case "question.v2.replied":
          case "question.rejected":
          case "question.v2.rejected":
            setWorkspace("pendingQuestions", (prev) =>
              prev.filter((q) => q.id !== evt.properties.requestID)
            );
            break;
          case "session.error":
            if (evt.properties.sessionID) {
              setWorkspace("statusBySession", evt.properties.sessionID, { type: "idle" });
              void loadSessionMessages(evt.properties.sessionID);
            }
            console.error("session error:", evt.properties);
            break;
        }
      },
    });

    onCleanup(disconnect);
  });

  return <ThreePaneShell />;
}
