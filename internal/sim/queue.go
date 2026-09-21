// Package sim is the discrete-event core: a priority queue of events, the
// World they mutate, and Run, which drives a controller through a scenario.
package sim

import "container/heap"

// Event is anything scheduled on the simulation clock. Events fire in
// (At, Seq) order; Seq is assigned at push time so ties resolve in the order
// they were scheduled.
type Event interface {
	At() int64
	Seq() uint64
	stamp(seq uint64)
}

// timing is embedded by every concrete event.
type timing struct {
	at  int64
	seq uint64
}

func at(t int64) timing { return timing{at: t} }

// At is the simulation time the event fires.
func (t timing) At() int64 { return t.at }

// Seq is the push-order tie breaker.
func (t timing) Seq() uint64 { return t.seq }

func (t *timing) stamp(seq uint64) { t.seq = seq }

type eventHeap []Event

func (h eventHeap) Len() int { return len(h) }
func (h eventHeap) Less(i, j int) bool {
	if h[i].At() != h[j].At() {
		return h[i].At() < h[j].At()
	}
	return h[i].Seq() < h[j].Seq()
}
func (h eventHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *eventHeap) Push(x any)   { *h = append(*h, x.(Event)) }
func (h *eventHeap) Pop() any {
	old := *h
	e := old[len(old)-1]
	*h = old[:len(old)-1]
	return e
}

// queue is the event queue. It is not safe for concurrent use and never
// needs to be: the core is single-threaded by design.
type queue struct {
	h    eventHeap
	next uint64
}

func (q *queue) push(e Event) {
	q.next++
	e.stamp(q.next)
	heap.Push(&q.h, e)
}

func (q *queue) pop() Event { return heap.Pop(&q.h).(Event) }

func (q *queue) len() int { return len(q.h) }
