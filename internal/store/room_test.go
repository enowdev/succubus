package store

import (
	"strings"
	"testing"
)

func TestMentionExtraction(t *testing.T) {
	s := testStore(t)

	m, err := s.PostMessage("p1", "", MsgQuestion, "", "ORION",
		"Should @VESPER own this, or @kestrel? Not an email: a@b.com", nil)
	if err != nil {
		t.Fatal(err)
	}

	got := strings.Join(m.Mentions, ",")
	// Names are normalised to upper case; the address-like token is still
	// picked up, which is acceptable — a stray mention is harmless, a missed
	// one is not.
	if !strings.Contains(got, "VESPER") || !strings.Contains(got, "KESTREL") {
		t.Fatalf("expected VESPER and KESTREL in mentions, got %q", got)
	}
}

// TestMentionAllReachesEveryone is the property @ALL exists for.
func TestMentionAllReachesEveryone(t *testing.T) {
	s := testStore(t)
	a := liveAgent(t, s, "AA")
	b := liveAgent(t, s, "BB")

	if _, err := s.PostMessage("p1", "", MsgAnnounce, "", HumanAuthor,
		"@ALL stop touching the schema", nil); err != nil {
		t.Fatal(err)
	}

	for _, ag := range []*Agent{a, b} {
		_, mentions, err := s.UnreadFor("p1", ag.Name, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(mentions) != 1 {
			t.Fatalf("%s should see the @ALL message as a mention, got %d", ag.Name, len(mentions))
		}
	}
}

// TestUnreadExcludesOwnMessages: an agent should not be told about its own post.
func TestUnreadExcludesOwnMessages(t *testing.T) {
	s := testStore(t)
	liveAgent(t, s, "AA")

	if _, err := s.PostMessage("p1", "", MsgMessage, "", "AA", "thinking out loud", nil); err != nil {
		t.Fatal(err)
	}
	unread, mentions, err := s.UnreadFor("p1", "AA", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(unread)+len(mentions) != 0 {
		t.Fatalf("an agent must not see its own message as unread, got %d", len(unread)+len(mentions))
	}
}

// TestMarkRoomReadStopsRepeats: injection must not repeat what it already showed.
func TestMarkRoomReadStopsRepeats(t *testing.T) {
	s := testStore(t)
	liveAgent(t, s, "AA")

	s.PostMessage("p1", "", MsgMessage, "", "BB", "first", nil)
	unread, _, _ := s.UnreadFor("p1", "AA", 10)
	if len(unread) != 1 {
		t.Fatalf("expected 1 unread, got %d", len(unread))
	}

	if err := s.MarkRoomRead("p1", "AA"); err != nil {
		t.Fatal(err)
	}
	unread, _, _ = s.UnreadFor("p1", "AA", 10)
	if len(unread) != 0 {
		t.Fatalf("marked read, but still %d unread", len(unread))
	}

	// Anything posted afterwards is new again.
	s.PostMessage("p1", "", MsgMessage, "", "BB", "second", nil)
	unread, _, _ = s.UnreadFor("p1", "AA", 10)
	if len(unread) != 1 {
		t.Fatalf("expected the later message to be unread, got %d", len(unread))
	}
}

// TestReplyToQuestionBecomesAnswer covers the implicit kind promotion.
func TestReplyToQuestionBecomesAnswer(t *testing.T) {
	s := testStore(t)

	q, err := s.PostMessage("p1", "", MsgQuestion, "", "AA", "which way?", nil)
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.PostMessage("p1", q.ID, "", "", "BB", "that way", nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Kind != MsgAnswer {
		t.Fatalf("a reply to a question should be an answer, got %q", r.Kind)
	}

	// Threading: the reply comes back attached to its parent, not standalone.
	msgs, err := s.RoomMessages("p1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 top-level message, got %d", len(msgs))
	}
	if len(msgs[0].Replies) != 1 {
		t.Fatalf("expected the reply nested under the question, got %d", len(msgs[0].Replies))
	}
}

func TestOpenQuestionsAndResolve(t *testing.T) {
	s := testStore(t)

	q, _ := s.PostMessage("p1", "", MsgQuestion, "", "AA", "unanswered?", nil)
	open, err := s.OpenQuestions("p1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 1 {
		t.Fatalf("expected 1 open question, got %d", len(open))
	}

	if err := s.ResolveQuestion(q.ID, "BB"); err != nil {
		t.Fatal(err)
	}
	open, _ = s.OpenQuestions("p1", 10)
	if len(open) != 0 {
		t.Fatalf("resolved question still listed as open (%d)", len(open))
	}

	// Resolving twice is not an error the caller should have to guard against,
	// but it must not silently claim success either.
	if err := s.ResolveQuestion(q.ID, "BB"); err == nil {
		t.Fatal("resolving an already-resolved question should report not-found")
	}
}
