package ast

import "fmt"

type Kind int

const (
	// meta
	Invalid Kind = iota
	Comment
	Key
	Symbol
	WhiteSpace

	// top level structures
	Table
	ArrayTable
	KeyValue

	// containers values
	Array
	InlineTable

	// values
	String
	Bool
	Float
	Integer
	LocalDate
	LocalDateTime
	DateTime
	Time
)

func (k Kind) String() string {
	switch k {
	case Invalid:
		return "Invalid"
	case Comment:
		return "Comment"
	case Symbol:
		return "Symbol"
	case WhiteSpace:
		return "WhiteSpace"
	case Key:
		return "Key"
	case Table:
		return "Table"
	case ArrayTable:
		return "ArrayTable"
	case KeyValue:
		return "KeyValue"
	case Array:
		return "Array"
	case InlineTable:
		return "InlineTable"
	case String:
		return "String"
	case Bool:
		return "Bool"
	case Float:
		return "Float"
	case Integer:
		return "Integer"
	case LocalDate:
		return "LocalDate"
	case LocalDateTime:
		return "LocalDateTime"
	case DateTime:
		return "DateTime"
	case Time:
		return "Time"
	}
	panic(fmt.Errorf("Kind.String() not implemented for '%d'", k))
}
