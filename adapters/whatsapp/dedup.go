package whatsapp

// dedup remembers recent message IDs so a redelivery is ignored. It is not
// safe for concurrent use; the event handler serializes access.
type dedup struct {
	capacity int
	seen     map[string]struct{}
	order    []string
}

func newDedup(capacity int) *dedup {
	if capacity <= 0 {
		capacity = 1024
	}
	return &dedup{capacity: capacity, seen: make(map[string]struct{}, capacity)}
}

func (d *dedup) fresh(id string) bool {
	if id == "" {
		return true
	}
	if _, ok := d.seen[id]; ok {
		return false
	}
	d.seen[id] = struct{}{}
	d.order = append(d.order, id)
	if len(d.order) > d.capacity {
		oldest := d.order[0]
		d.order = d.order[1:]
		delete(d.seen, oldest)
	}
	return true
}
