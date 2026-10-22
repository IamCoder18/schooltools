package cmd

import "sync"

const relatedWorkers = 6

type fetchResult[T any] struct {
	value T
	err   error
}

func fetchAll[T any](n int, fn func(int) (T, error)) []fetchResult[T] {
	out := make([]fetchResult[T], n)
	if n == 0 {
		return out
	}
	sem := make(chan struct{}, relatedWorkers)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			out[i].value, out[i].err = fn(i)
		}(i)
	}
	wg.Wait()
	return out
}
