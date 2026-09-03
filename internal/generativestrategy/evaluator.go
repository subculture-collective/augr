package generativestrategy

import (
	"fmt"

	"github.com/shopspring/decimal"
)

type expressionValue struct {
	kind    string
	decimal decimal.Decimal
	boolean bool
}

func evaluateExpressions(entry, exit exprCanonical, types map[string]string, raw map[string]string) (bool, bool, error) {
	if len(raw) != len(types) {
		return false, false, fmt.Errorf("generated strategy evaluation requires every declared input exactly once")
	}
	values := make(map[string]expressionValue, len(types))
	for name, kind := range types {
		encoded, ok := raw[name]
		if !ok {
			return false, false, fmt.Errorf("generated strategy evaluation input %q is missing", name)
		}
		switch kind {
		case "decimal":
			value, err := exactDecimal(encoded)
			if err != nil {
				return false, false, fmt.Errorf("generated strategy evaluation input %q: %w", name, err)
			}
			values[name] = expressionValue{kind: kind, decimal: value}
		case "boolean":
			if encoded != "true" && encoded != "false" {
				return false, false, fmt.Errorf("generated strategy evaluation input %q is not canonical boolean", name)
			}
			values[name] = expressionValue{kind: kind, boolean: encoded == "true"}
		default:
			return false, false, fmt.Errorf("generated strategy evaluation input %q has unsupported type", name)
		}
	}
	for name := range raw {
		if _, ok := types[name]; !ok {
			return false, false, fmt.Errorf("generated strategy evaluation input %q is undeclared", name)
		}
	}
	entryValue, err := evaluateExpression(entry, values)
	if err != nil || entryValue.kind != "boolean" {
		return false, false, fmt.Errorf("evaluate generated strategy entry: %w", err)
	}
	exitValue, err := evaluateExpression(exit, values)
	if err != nil || exitValue.kind != "boolean" {
		return false, false, fmt.Errorf("evaluate generated strategy exit: %w", err)
	}
	return entryValue.boolean, exitValue.boolean, nil
}

func evaluateExpression(expr exprCanonical, values map[string]expressionValue) (expressionValue, error) {
	switch expr.Op {
	case "ref":
		value, ok := values[expr.Ref]
		if !ok {
			return expressionValue{}, fmt.Errorf("reference %q is missing", expr.Ref)
		}
		return value, nil
	case "decimal":
		value, err := exactDecimal(expr.Value)
		return expressionValue{kind: "decimal", decimal: value}, err
	case "boolean":
		return expressionValue{kind: "boolean", boolean: expr.Value == "true"}, nil
	case "not":
		value, err := evaluateExpression(expr.Args[0], values)
		if err != nil {
			return expressionValue{}, err
		}
		return expressionValue{kind: "boolean", boolean: !value.boolean}, nil
	}
	left, err := evaluateExpression(expr.Args[0], values)
	if err != nil {
		return expressionValue{}, err
	}
	right, err := evaluateExpression(expr.Args[1], values)
	if err != nil {
		return expressionValue{}, err
	}
	switch expr.Op {
	case "add":
		return expressionValue{kind: "decimal", decimal: left.decimal.Add(right.decimal)}, nil
	case "sub":
		return expressionValue{kind: "decimal", decimal: left.decimal.Sub(right.decimal)}, nil
	case "mul":
		return expressionValue{kind: "decimal", decimal: left.decimal.Mul(right.decimal)}, nil
	case "div":
		if right.decimal.IsZero() {
			return expressionValue{}, fmt.Errorf("division by zero")
		}
		return expressionValue{kind: "decimal", decimal: left.decimal.Div(right.decimal)}, nil
	case "lt":
		return expressionValue{kind: "boolean", boolean: left.decimal.LessThan(right.decimal)}, nil
	case "lte":
		return expressionValue{kind: "boolean", boolean: left.decimal.LessThanOrEqual(right.decimal)}, nil
	case "gt":
		return expressionValue{kind: "boolean", boolean: left.decimal.GreaterThan(right.decimal)}, nil
	case "gte":
		return expressionValue{kind: "boolean", boolean: left.decimal.GreaterThanOrEqual(right.decimal)}, nil
	case "eq":
		return expressionValue{kind: "boolean", boolean: left.decimal.Equal(right.decimal)}, nil
	case "and":
		return expressionValue{kind: "boolean", boolean: left.boolean && right.boolean}, nil
	case "or":
		return expressionValue{kind: "boolean", boolean: left.boolean || right.boolean}, nil
	default:
		return expressionValue{}, fmt.Errorf("unsupported operator %q", expr.Op)
	}
}
