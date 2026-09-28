package eval

import "math/big"

// bigRat aliases math/big.Rat so the test helpers read cleanly.
type bigRat = big.Rat

func newRat(n int64) *big.Rat { return big.NewRat(n, 1) }
