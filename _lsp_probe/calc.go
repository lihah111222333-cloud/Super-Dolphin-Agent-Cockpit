package probe

import "fmt"

// Calculator provides basic math ops.
type Calculator struct {
	Result float64
}

// Add adds n to result.
func (c *Calculator) Add(n float64) float64 {
	c.Result += n
	return c.Result
}

// Sub subtracts n from result.
func (c *Calculator) Sub(n float64) float64 {
	c.Result -= n
	return c.Result
}

// Mul multiplies result by n.
func (c *Calculator) Mul(n float64) float64 {
	c.Result *= n
	return c.Result
}

// Hello prints a greeting.
func Hello(name string) string {
	return fmt.Sprintf("hello %s", name)
}
