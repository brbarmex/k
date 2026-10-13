package domain

import "testing"

func TestOnlyLeaderMsgAppBeforeSync(t *testing.T) {
	all := []MsgKind{MsgApp, MsgAppResp, MsgVote, MsgVoteResp, MsgPreVote, MsgPreVoteResp,
		MsgHeartbeat, MsgHeartbeatResp, MsgSnap, MsgOther}
	for _, k := range all {
		if CanSendBeforeSync(false, k) {
			t.Errorf("seguidor ou learner não pode enviar %d antes do fsync", k)
		}
		if want := k == MsgApp; CanSendBeforeSync(true, k) != want {
			t.Errorf("líder, tipo %d: esperado %v", k, want)
		}
	}
}

type msg struct {
	id   int
	kind MsgKind
}

func TestSplitKeepsOrder(t *testing.T) {
	msgs := []msg{{1, MsgApp}, {2, MsgAppResp}, {3, MsgApp}, {4, MsgVoteResp}, {5, MsgHeartbeat}}
	kind := func(m msg) MsgKind { return m.kind }

	before, after := SplitBeforeSync(true, msgs, kind)
	if len(before) != 2 || before[0].id != 1 || before[1].id != 3 {
		t.Fatalf("antes do fsync: %v", before)
	}
	if len(after) != 3 || after[0].id != 2 || after[1].id != 4 || after[2].id != 5 {
		t.Fatalf("depois do fsync: %v", after)
	}

	// Um learner da Azure nunca antecipa nada, nem MsgApp (não deveria gerar, mas a regra vale).
	before, after = SplitBeforeSync(false, msgs, kind)
	if len(before) != 0 || len(after) != len(msgs) {
		t.Fatalf("não líder: antes=%v depois=%v", before, after)
	}
}
