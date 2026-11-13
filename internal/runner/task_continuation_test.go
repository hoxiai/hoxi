package runner

import "testing"

func TestTaskContinuationSurvivesUpstreamUpdatesAndReload(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store := NewStore(dir)
	const sid = "ses_hoxi_task_continuation"
	if err := store.Put(SessionRecord{ID: sid, Runtime: "opencode", ProjectDir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	store.UpsertTurnInfo(sid, MessageInfo{ID: "msg_original", SessionID: sid, Role: "user"})
	store.UpsertTurnInfo(sid, MessageInfo{ID: "msg_continue", SessionID: sid, Role: "user", ContinueTask: true})
	// OpenCode's message.updated and post-turn snapshots omit Hoxi-only fields.
	store.UpsertTurnInfo(sid, MessageInfo{ID: "msg_continue", SessionID: sid, Role: "user", Agent: "build"})
	store.UpsertTurnInfo(sid, MessageInfo{ID: "msg_new_task", SessionID: sid, Role: "user"})
	store.Flush()

	for _, current := range []*Store{store, NewStore(dir)} {
		rec, ok := current.Get(sid)
		if !ok || len(rec.Turns) != 3 {
			t.Fatalf("missing turns: %+v", rec.Turns)
		}
		if rec.Turns[0].Info.ContinueTask || !rec.Turns[1].Info.ContinueTask || rec.Turns[2].Info.ContinueTask {
			t.Fatalf("task boundaries lost: %+v", rec.Turns)
		}
		if rec.Turns[1].Info.Agent != "build" {
			t.Fatal("preserving task metadata dropped the upstream update")
		}
	}
}
