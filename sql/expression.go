package sql

type Expression interface {
	Resolvable
	Type() Type
	Name() string
	Eval(Row) interface{}
	TransformUp(func(Expression) Expression) Expression
}
