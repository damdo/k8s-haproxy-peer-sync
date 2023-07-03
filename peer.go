package main

type Peer struct {
	addresses []string
	hostname  string
}

// difference returns the Peers in `a` that aren't in `b`.
func difference(a []Peer, b []Peer) []Peer {
	mb := make(map[string]struct{}, len(b))
	for _, x := range b {
		mb[x.hostname] = struct{}{}
	}
	var diff []Peer
	for _, x := range a {
		if _, found := mb[x.hostname]; !found {
			diff = append(diff, x)
		}
	}
	return diff
}
