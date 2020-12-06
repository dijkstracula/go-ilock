# go-ilock

A simple intention lock implementation for Go.

## Testing

The test framework includes some randomised load tests
that are worth running with the race detector on.

```
$ go test -race -v
```

## Benchmarking


```
$ go test -v -bench=. -benchtime=1000000x
BenchmarkSerialNoWrites
BenchmarkSerialNoWrites-12                	 1000000	      6680 ns/op
BenchmarkSerial
BenchmarkSerial-12                        	 1000000	      6665 ns/op
BenchmarkSerialHeavyLocking
BenchmarkSerialHeavyLocking-12            	 1000000	      6680 ns/op
BenchmarkLowConcurrency
BenchmarkLowConcurrency-12                	 1000000	      4788 ns/op
BenchmarkMediumConcurrency
BenchmarkMediumConcurrency-12             	 1000000	      5355 ns/op
BenchmarkHighConcurrencyNoWrites
BenchmarkHighConcurrencyNoWrites-12       	 1000000	      5741 ns/op
BenchmarkHighConcurrency
BenchmarkHighConcurrency-12               	 1000000	      5804 ns/op
BenchmarkHighConcurrencyHeavyWrites
BenchmarkHighConcurrencyHeavyWrites-12    	 1000000	      5972 ns/op
```
