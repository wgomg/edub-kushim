package concurrency

type Semaphore struct {
	slots chan struct{}
}

func NewSemaphore(n int) *Semaphore {
	if n < 1 {
		n = 1
	}
	return &Semaphore{
		slots: make(chan struct{}, n),
	}
}

func (s *Semaphore) Acquire() bool {
	select {
	case s.slots <- struct{}{}:
		return true
	default:
		return false
	}
}

func (s *Semaphore) Release() {
	<-s.slots
}
