# go-ilock

A simple intention lock implementation for Go.

## Testing

The test framework includes some randomised load tests
that are worth running with the race detector on.

```
$ go test -race -v
```

## Benchmarking

Currently the lock does not favour writers.  I'll get to that sometime.

```
$ go test -v -bench=.
BenchmarkSerialNoWrites
BenchmarkSerialNoWrites-12                	  802267	      1470 ns/op
BenchmarkSerial
BenchmarkSerial-12                        	  801068	      1530 ns/op
BenchmarkSerialHeavyLocking
BenchmarkSerialHeavyLocking-12            	  586545	      1974 ns/op

BenchmarkLowConcurrency
BenchmarkLowConcurrency-12                	  870514	      1373 ns/op

BenchmarkMediumConcurrency
BenchmarkMediumConcurrency-12             	 1365499	       884 ns/op

BenchmarkHighConcurrencyNoWrites
BenchmarkHighConcurrencyNoWrites-12       	 1538413	       787 ns/op
BenchmarkHighConcurrency
BenchmarkHighConcurrency-12               	 1390474	       937 ns/op
BenchmarkHighConcurrencyHeavyWrites
BenchmarkHighConcurrencyHeavyWrites-12    	  695355	      1648 ns/op
```
