package sim

import "testing"

func TestQueueOrdersByTimeThenSeq(t *testing.T) {
	var q queue
	q.push(&SimEnd{at(10)})
	q.push(&CheckpointTick{at(5)})
	q.push(&ControllerTick{at(5)})
	q.push(&MetricsSample{at(1)})
	q.push(&BoxArrive{timing: at(5), Box: "box-1"})

	var got []struct {
		at  int64
		seq uint64
	}
	for q.len() > 0 {
		e := q.pop()
		got = append(got, struct {
			at  int64
			seq uint64
		}{e.At(), e.Seq()})
	}
	want := []struct {
		at  int64
		seq uint64
	}{{1, 4}, {5, 2}, {5, 3}, {5, 5}, {10, 1}}
	if len(got) != len(want) {
		t.Fatalf("got %d events, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("event %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}
