package leader

import (
	// "fmt"
	"sync"
	"time"

	"github.com/chrislusf/glow/resource"
	"github.com/chrislusf/glow/util"
)

const TimeOutLimit = 15 // seconds

type ResourceUpdateEvent struct {
	DataCenter string
	Rack       string
}

type LeaderResource struct {
	Topology      resource.Topology
	EventChan     chan interface{}
	lock          sync.Mutex
	EvictionQueue *util.PriorityQueue
}

func NewLeaderResource() *LeaderResource {
	l := &LeaderResource{
		Topology: resource.Topology{
			DataCenters: make(map[string]*resource.DataCenter),
		},
		EventChan: make(chan interface{}, 1),
		EvictionQueue: util.NewPriorityQueue(func(a, b interface{}) bool {
			x, y := a.(*resource.AgentInformation), b.(*resource.AgentInformation)
			return x.LastHeartBeat.Before(y.LastHeartBeat)
		}),
	}

	go l.BackgroundEventLoop()
	go l.BackgroundEvictionLoop()

	return l
}

func (l *LeaderResource) UpdateAgentInformation(ai *resource.AgentInformation) {
	l.lock.Lock()
	defer l.lock.Unlock()

	dc, hasDc := l.Topology.DataCenters[ai.Location.DataCenter]
	if !hasDc {
		dc = &resource.DataCenter{
			Name:  ai.Location.DataCenter,
			Racks: make(map[string]*resource.Rack),
		}
		l.Topology.DataCenters[ai.Location.DataCenter] = dc
	}

	rack, hasRack := dc.Racks[ai.Location.Rack]
	if !hasRack {
		rack = &resource.Rack{
			Name:   ai.Location.Rack,
			Agents: make(map[string]*resource.AgentInformation),
		}
		dc.Racks[ai.Location.Rack] = rack
	}

	oldInfo, hasOldInfo := rack.Agents[ai.Location.URL()]
	deltaResource := ai.Resource
	// fmt.Printf("hasOldInfo %+v, oldInfo %+v\n", hasOldInfo, oldInfo)
	if hasOldInfo {
		deltaResource = deltaResource.Minus(oldInfo.Resource)
		if !deltaResource.IsZero() {
			oldInfo.Resource = ai.Resource
		}
		oldInfo.LastHeartBeat = time.Now()
		l.EvictionQueue.Enqueue(ai, 0)
	} else {
		rack.Agents[ai.Location.URL()] = ai
		ai.LastHeartBeat = time.Now()
		l.EvictionQueue.Enqueue(ai, 0)
	}

	if !deltaResource.IsZero() {
		// fmt.Printf("updating %+v\n", deltaResource)
		l.EventChan <- ResourceUpdateEvent{ai.Location.DataCenter, ai.Location.Rack}

		rack.Resource = rack.Resource.Plus(deltaResource)
		dc.Resource = dc.Resource.Plus(deltaResource)
		l.Topology.Resource = l.Topology.Resource.Plus(deltaResource)
	}

	if hasOldInfo {
		deltaAllocated := ai.Allocated.Minus(oldInfo.Allocated)
		if !deltaAllocated.IsZero() {
			rack.Allocated = rack.Allocated.Plus(deltaAllocated)
			dc.Allocated = dc.Allocated.Plus(deltaAllocated)
			l.Topology.Allocated = l.Topology.Allocated.Plus(deltaAllocated)
		}
	}

}

func (l *LeaderResource) deleteAgentInformation(ai *resource.AgentInformation) {
	l.lock.Lock()
	defer l.lock.Unlock()

	dc, hasDc := l.Topology.DataCenters[ai.Location.DataCenter]
	if !hasDc {
		return
	}

	rack, hasRack := dc.Racks[ai.Location.Rack]
	if !hasRack {
		return
	}

	oldInfo, hasOldInfo := rack.Agents[ai.Location.URL()]
	if !hasOldInfo {
		return
	}

	deltaResource := oldInfo.Resource
	deltaAllocated := oldInfo.Allocated

	if !deltaResource.IsZero() {
		// fmt.Printf("deleting %+v\n", oldInfo)
		delete(rack.Agents, ai.Location.URL())
		l.EventChan <- ResourceUpdateEvent{ai.Location.DataCenter, ai.Location.Rack}

		rack.Resource = rack.Resource.Minus(deltaResource)
		rack.Allocated = rack.Allocated.Minus(deltaAllocated)
		dc.Resource = dc.Resource.Minus(deltaResource)
		dc.Allocated = dc.Allocated.Minus(deltaAllocated)
		l.Topology.Resource = l.Topology.Resource.Minus(deltaResource)
		l.Topology.Allocated = l.Topology.Allocated.Minus(deltaAllocated)
	}

}

func (l *LeaderResource) findAgentInformation(location resource.Location) (*resource.AgentInformation, bool) {
	d, hasDc := l.Topology.DataCenters[location.DataCenter]
	if !hasDc {
		return nil, false
	}

	r, hasRack := d.Racks[location.Rack]
	if !hasRack {
		return nil, false
	}

	ai, ok := r.Agents[location.URL()]
	return ai, ok
}

func (l *LeaderResource) BackgroundEvictionLoop() {
	for {
		if l.EvictionQueue.Len() == 0 {
			// println("eviction: sleep for", TimeOutLimit, "seconds")
			time.Sleep(TimeOutLimit * time.Second)
			continue
		}
		obj, _ := l.EvictionQueue.Dequeue()
		a := obj.(*resource.AgentInformation)
		ai, found := l.findAgentInformation(a.Location)
		if !found {
			continue
		}
		if ai.LastHeartBeat.Add(TimeOutLimit * time.Second).After(time.Now()) {
			// println("eviction 1: sleep for", ai.LastHeartBeat.Add(TimeOutLimit*time.Second).Sub(time.Now()))
			time.Sleep(ai.LastHeartBeat.Add(TimeOutLimit * time.Second).Sub(time.Now()))
		}
		ai, found = l.findAgentInformation(ai.Location)
		if !found {
			continue
		}
		if ai.LastHeartBeat.Add(TimeOutLimit * time.Second).Before(time.Now()) {
			// this has not been refreshed since last heart beat
			l.deleteAgentInformation(ai)
		}
	}
}

func (l *LeaderResource) BackgroundEventLoop() {
	for {
		event := <-l.EventChan
		switch event.(type) {
		default:
		case ResourceUpdateEvent:
			// fmt.Printf("update %s:%s\n", event.DataCenter, event.Rack)
		}
	}
}