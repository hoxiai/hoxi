import { onCleanup, onMount } from "solid-js";
import ThreePaneShell from "./components/layout/ThreePaneShell";
import { resolveParentSessionID } from "./components/timeline/SubagentSessionBanner";
import { hoxiAPI } from "./services/api";
import { connectEventStream } from "./services/sse";
import {
  appSettings,
  applySessionDiffEvent,
  applyPartDeltaBatch,
  applyStreamFrameBatch,
  bootstrapProjectSessions,
  drainNextQueuedPrompt,
  handleIncomingPermissionRequest,
  handleSessionErrorEvent,
  isDismissedProjectPath,
  isInvalidProjectPath,
  loadAgents,
  loadProjects,
  loadProviders,
  loadProviderTelemetry,
  loadSavedWorkspaces,
  loadSessionMessages,
  loadSessionMessagesOnly,
  normalizeFolderPath,
  recordSessionRetry,
  removeSessionFromStore,
  replaceSession,
  resolveDefaultProjectDir,
  runSessionCodeAudit,
  scheduleRefreshWorktreeStatus,
  selectSession,
  setWorkspace,
  syncApprovalModeToOpenCodeConfig,
  syncPendingPermissions,
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
        appVersion: boot.appVersion,
        rootDir: bootRoot,
        wsBaseURL: boot.wsBaseURL,
      });

      const eagerBootRoot =
        bootRoot && !isDismissedProjectPath(bootRoot) ? bootRoot : "";
      const eagerSessionsPromise = eagerBootRoot
        ? bootstrapProjectSessions(eagerBootRoot).catch((err) => {
            console.warn("Initial session load retry pending:", err);
            return null;
          })
        : null;

      // Kick off non-blocking background loads while awaiting saved workspaces & projects
      void loadProviders();
      void loadProviderTelemetry();
      void loadAgents();
      void syncApprovalModeToOpenCodeConfig(appSettings.approvalMode);
      void syncPendingPermissions(eagerBootRoot || undefined);
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
        if (eagerSessionsPromise && activeRoot === eagerBootRoot) {
          await eagerSessionsPromise;
          if (workspace.sessions.length === 0) {
            await bootstrapProjectSessions(activeRoot);
          }
        } else {
          await bootstrapProjectSessions(activeRoot);
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
          void syncPendingPermissions();
          if (
            workspace.rootDir &&
            workspace.sessions.length === 0 &&
            !workspace.bootstrappingSessions
          ) {
            void bootstrapProjectSessions(workspace.rootDir).catch(() => {});
          }
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
            const previousStatus = workspace.statusBySession[sid]?.type;
            const wasBusy = previousStatus === "busy" || previousStatus === "retry";
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
              } else if (wasBusy) {
                void loadSessionMessages(sid).catch(console.error);
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
            applySessionDiffEvent(evt.properties.sessionID, evt.properties.diff);
            if (evt.properties.sessionID === workspace.activeSessionID) {
              scheduleRefreshWorktreeStatus(evt.properties.sessionID, 600);
            }
            break;
          case "permission.asked":
            handleIncomingPermissionRequest(evt.properties);
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
              void handleSessionErrorEvent(evt.properties.sessionID, evt.properties.error as any);
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
