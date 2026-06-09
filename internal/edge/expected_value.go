package edge

// BinaryEVInput describes a binary outcome trade setup.
type BinaryEVInput struct {
	Probability float64
	Price       float64
	Fee         float64
	Slippage    float64
	ExitHaircut float64
}

// BinaryEVResult contains the gross/net EV and raw edge.
type BinaryEVResult struct {
	GrossEV float64
	NetEV   float64
	Edge    float64
}

// BinaryNetEV computes gross and net EV for a binary contract.
func BinaryNetEV(in BinaryEVInput) BinaryEVResult {
	gross := in.Probability*(1-in.Price) - (1-in.Probability)*in.Price
	costs := in.Fee + in.Slippage + in.ExitHaircut
	return BinaryEVResult{
		GrossEV: gross,
		NetEV:   gross - costs,
		Edge:    in.Probability - in.Price,
	}
}

// OptionEdgeInput describes a modeled option edge.
type OptionEdgeInput struct {
	ModelPrice      float64
	ExecutablePrice float64
	Commission      float64
	Slippage        float64
	ModelHaircut    float64
}

// OptionEdgeResult contains gross and net option edge.
type OptionEdgeResult struct {
	GrossEdge float64
	NetEdge   float64
}

// OptionEdge computes gross and net edge for a model-vs-executable price.
func OptionEdge(in OptionEdgeInput) OptionEdgeResult {
	gross := in.ModelPrice - in.ExecutablePrice
	return OptionEdgeResult{
		GrossEdge: gross,
		NetEdge:   gross - in.Commission - in.Slippage - in.ModelHaircut,
	}
}
