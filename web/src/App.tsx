import { onCleanup, onMount } from "solid-js";
import ThreePaneShell from "./components/layout/ThreePaneShell";
import { resolveParentSessionID } from "./components/timeline/SubagentSessionBanner";
import { hoxiAPI } from "./services/api";
import { connectEventStream } from "./services/sse";
import {
  applyPartDeltaBatch,
  applyStreamFrameBatch,
  drainNextQueuedPrompt,
  isInvalidProjectPath,
  isSessionArchived,
  loadAgents,
  loadProjects,
  loadProviders,
  loadProviderTelemetry,
  loadSavedWorkspaces,
  loadSessionMessages,
  loadSessionMessagesOnly,
  normalizeFolderPath,
  recordSessionRetry,
  refreshWorktreeStatus,
  removeSessionFromStore,
  replaceSession,
  resolveDefaultProjectDir,
  runSessionCodeAudit,
  scheduleRefreshWorktreeStatus,
  selectSession,
  setWorkspace,
  sortSessionsByActivity,
  syncSessionStatuses,
  triggerDesktopNotification,
  upsertMessageInfo,
  upsertMessagePart,
  workspace,
} from "./store/workspace";
import { t } from "./i18n";

export default function App() {
  onMount(async () => {
    try {
      const boot = await hoxiAPI.getBootstrap();
      const bootRoot = isInvalidProjectPath(boot.rootDir)
        ? ""
        : normalizeFolderPath(boot.rootDir);
      setWorkspace({
        rootDir: bootRoot,
        wsBaseURL: boot.wsBaseURL,
      });

      // Kick off non-blocking background loads while awaiting saved workspaces & projects
      void loadProviders();
      void loadProviderTelemetry();
      void loadAgents();
      void hoxiAPI
        .listQuestions()
        .then((qs) => {
          if (Array.isArray(qs)) setWorkspace("pendingQuestions", qs);
        })
        .catch(() => {});

      await Promise.all([loadSavedWorkspaces(), loadProjects()]);

      const activeRoot = resolveDefaultProjectDir(bootRoot);
      if (activeRoot && activeRoot !== workspace.rootDir) {
        setWorkspace("rootDir", activeRoot);
      }

      if (activeRoot) {
        void refreshWorktreeStatus("__root__");
        void syncSessionStatuses(activeRoot);

        const sessions = await hoxiAPI.listSessions(activeRoot);
        setWorkspace("sessions", sessions);

        const unarchived = sessions.filter((s) => !s.parentID && !isSessionArchived(s));
        if (unarchived.length > 0) {
          const mostRecent = sortSessionsByActivity(unarchived)[0];
          const p = selectSession(mostRecent.id);
          setWorkspace("bootstrappingSessions", false);
          await p;
        } else {
          setWorkspace("bootstrappingSessions", false);
        }
      } else {
        setWorkspace("bootstrappingSessions", false);
      }
    } catch (err) {
      setWorkspace("bootstrappingSessions", false);
      console.error("Bootstrap error:", err);
    }

    const disconnect = connectEventStream({
      onConnectionChange: (connected) => {
        setWorkspace("connected", connected);
        if (connected) {
          void loadProviders();
          void loadProviderTelemetry();
          void syncSessionStatuses();
        }
      },
      onDeltaBatch: applyPartDeltaBatch,
      onFrameBatch: applyStreamFrameBatch,
      onEvent: (evt) => {
        switch (evt.type) {
          case "message.updated":
            upsertMessageInfo(evt.properties.info);
            break;
          case "message.part.updated":
            upsertMessagePart(evt.properties.part);
            break;
          case "session.updated":
          case "session.created":
            replaceSession(evt.properties.info);
            break;
          case "session.deleted": {
            const delID = evt.properties.sessionID || evt.properties.info?.id;
            if (delID) {
              const wasActive = workspace.activeSessionID === delID;
              const nextCandidate = removeSessionFromStore(delID);
              if (wasActive) {
                if (nextCandidate) {
                  void selectSession(nextCandidate.id);
                } else {
                  setWorkspace("activeSessionID", null);
                }
              }
            }
            break;
          }
          case "session.status": {
            const sid = evt.properties.sessionID;
            const wasBusy = workspace.statusBySession[sid]?.type === "busy";
            setWorkspace("statusBySession", sid, evt.properties.status);
            if (evt.properties.status.type === "busy") {
              setWorkspace("unreadCompletedBySession", sid, false);
            } else if (evt.properties.status.type === "idle") {
              setWorkspace("sessionFinishedAt", sid, Date.now());
              const parentID = resolveParentSessionID(sid);
              if (wasBusy && parentID) {
                void loadSessionMessagesOnly(sid);
                void loadSessionMessagesOnly(parentID);
                scheduleRefreshWorktreeStatus(parentID, 600);
              }
              if (
                wasBusy &&
                !parentID &&
                sid !== workspace.activeSessionID &&
                !workspace.interruptedBySession[sid]
              ) {
                setWorkspace("unreadCompletedBySession", sid, true);
              }
              void drainNextQueuedPrompt(sid);
            } else if (evt.properties.status.type === "retry") {
              recordSessionRetry(
                sid,
                evt.properties.status.attempt,
                evt.properties.status.message
              );
            }
            break;
          }
          case "session.idle": {
            const sid = evt.properties.sessionID;
            setWorkspace("statusBySession", sid, { type: "idle" });
            setWorkspace("sessionFinishedAt", sid, Date.now());
            const parentID = resolveParentSessionID(sid);
            if (parentID) {
              void loadSessionMessagesOnly(sid);
              void loadSessionMessagesOnly(parentID);
              scheduleRefreshWorktreeStatus(parentID, 600);
            } else {
              if (
                sid !== workspace.activeSessionID &&
                !workspace.interruptedBySession[sid]
              ) {
                setWorkspace("unreadCompletedBySession", sid, true);
              }
              triggerDesktopNotification("Hoxi", t("timeline.turnCompleted"));
              void loadSessionMessages(sid).then(() => {
                void drainNextQueuedPrompt(sid);
                void runSessionCodeAudit(sid, false);
              });
            }
            break;
          }
          case "session.diff":
            setWorkspace("diffsBySession", evt.properties.sessionID, evt.properties.diff);
            if (evt.properties.sessionID === workspace.activeSessionID) {
              scheduleRefreshWorktreeStatus(evt.properties.sessionID, 600);
            }
            break;
          case "permission.asked":
            setWorkspace("pendingPermissions", (prev) =>
              prev.some((p) => p.id === evt.properties.id) ? prev : [...prev, evt.properties]
            );
            triggerDesktopNotification("Hoxi", t("approval.title"));
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
            triggerDesktopNotification("Hoxi", t("approval.questionTitle"));
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
              setWorkspace("unreadCompletedBySession", evt.properties.sessionID, false);
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
