package media

const jitterResetThreshold = 50

type JitterFrame struct {
	Payload []byte
	Present bool
}

type JitterBuffer struct {
	depth   int
	next     uint16
	nextSet  bool
	maxSeen  uint16
	slots    map[uint16][]byte
}

func NewJitterBuffer(depth int) *JitterBuffer {
	return &JitterBuffer{
		depth: depth,
		slots: make(map[uint16][]byte),
	}
}

func (j *JitterBuffer) Push(seq uint16, payload []byte) []JitterFrame {
	if !j.nextSet {
		j.nextSet = true
		j.next = seq
		j.maxSeen = seq
		j.store(seq, payload)
		return j.drain()
	}

	d := int16(seq - j.next)
	if d > jitterResetThreshold || d < -jitterResetThreshold {
		clear(j.slots)
		j.next = seq
		j.maxSeen = seq
		j.store(seq, payload)
		return j.drain()
	}
	if d < 0 {
		return nil
	}
	j.store(seq, payload)
	if int16(seq-j.maxSeen) > 0 {
		j.maxSeen = seq
	}
	return j.drain()
}

func (j *JitterBuffer) store(seq uint16, payload []byte) {
	if _, ok := j.slots[seq]; ok {
		return
	}
	cp := make([]byte, len(payload))
	copy(cp, payload)
	j.slots[seq] = cp
}

func (j *JitterBuffer) drain() []JitterFrame {
	var out []JitterFrame
	for {
		if p, ok := j.slots[j.next]; ok {
			out = append(out, JitterFrame{Payload: p, Present: true})
			delete(j.slots, j.next)
			j.next++
			continue
		}
		if int16(j.maxSeen-j.next) >= int16(j.depth) {
			out = append(out, JitterFrame{Present: false})
			j.next++
			continue
		}
		break
	}
	return out
}
