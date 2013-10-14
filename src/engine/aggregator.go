package engine

import (
	"common"
	"fmt"
	"parser"
	"protocol"
	"time"
)

type Aggregator interface {
	AggregatePoint(series string, group interface{}, p *protocol.Point) error
	InitializeFieldsMetadata(series *protocol.Series) error
	GetValue(series string, group interface{}) *protocol.FieldValue
	ColumnName() string
	ColumnType() protocol.FieldDefinition_Type
}

type AggregatorInitializer func(*parser.Query, *parser.Value) (Aggregator, error)

var registeredAggregators = make(map[string]AggregatorInitializer)

func init() {
	registeredAggregators["count"] = NewCountAggregator
	registeredAggregators["max"] = NewMaxAggregator
	registeredAggregators["min"] = NewMinAggregator
	registeredAggregators["__timestamp_aggregator"] = NewTimestampAggregator
}

type CountAggregator struct {
	counts map[string]map[interface{}]int32
}

func (self *CountAggregator) AggregatePoint(series string, group interface{}, p *protocol.Point) error {
	counts := self.counts[series]
	if counts == nil {
		counts = make(map[interface{}]int32)
		self.counts[series] = counts
	}
	counts[group]++
	return nil
}

func (self *CountAggregator) ColumnName() string {
	return "count"
}

func (self *CountAggregator) ColumnType() protocol.FieldDefinition_Type {
	return protocol.FieldDefinition_INT32
}

func (self *CountAggregator) GetValue(series string, group interface{}) *protocol.FieldValue {
	value := self.counts[series][group]
	return &protocol.FieldValue{IntValue: &value}
}

func (self *CountAggregator) InitializeFieldsMetadata(series *protocol.Series) error { return nil }

func NewCountAggregator(*parser.Query, *parser.Value) (Aggregator, error) {
	return &CountAggregator{make(map[string]map[interface{}]int32)}, nil
}

type TimestampAggregator struct {
	duration   *time.Duration
	timestamps map[string]map[interface{}]int64
}

func (self *TimestampAggregator) AggregatePoint(series string, group interface{}, p *protocol.Point) error {
	timestamps := self.timestamps[series]
	if timestamps == nil {
		timestamps = make(map[interface{}]int64)
		self.timestamps[series] = timestamps
	}
	if self.duration != nil {
		timestamps[group] = time.Unix(*p.Timestamp, 0).Round(*self.duration).Unix()
	} else {
		timestamps[group] = *p.Timestamp
	}
	return nil
}

func (self *TimestampAggregator) ColumnName() string {
	return "count"
}

func (self *TimestampAggregator) ColumnType() protocol.FieldDefinition_Type {
	return protocol.FieldDefinition_INT32
}

func (self *TimestampAggregator) GetValue(series string, group interface{}) *protocol.FieldValue {
	value := self.timestamps[series][group]
	return &protocol.FieldValue{Int64Value: &value}
}

func (self *TimestampAggregator) InitializeFieldsMetadata(series *protocol.Series) error { return nil }

func NewTimestampAggregator(query *parser.Query, _ *parser.Value) (Aggregator, error) {
	duration, err := query.GetGroupByClause().GetGroupByTime()
	if err != nil {
		return nil, err
	}

	return &TimestampAggregator{
		timestamps: make(map[string]map[interface{}]int64),
		duration:   duration,
	}, nil
}

type MaxAggregator struct {
	fieldName  string
	fieldIndex int
	fieldType  protocol.FieldDefinition_Type
	values     map[string]map[interface{}]protocol.FieldValue
}

func (self *MaxAggregator) AggregatePoint(series string, group interface{}, p *protocol.Point) error {
	values := self.values[series]
	if values == nil {
		values = make(map[interface{}]protocol.FieldValue)
		self.values[series] = values
	}

	currentValue := values[group]

	switch self.fieldType {
	case protocol.FieldDefinition_INT64:
		if value := *p.Values[self.fieldIndex].Int64Value; currentValue.Int64Value == nil || *currentValue.Int64Value < value {
			currentValue.Int64Value = &value
		}
	case protocol.FieldDefinition_INT32:
		if value := *p.Values[self.fieldIndex].IntValue; currentValue.IntValue == nil || *currentValue.IntValue < value {
			currentValue.IntValue = &value
		}
	case protocol.FieldDefinition_DOUBLE:
		if value := *p.Values[self.fieldIndex].DoubleValue; currentValue.DoubleValue == nil || *currentValue.DoubleValue < value {
			currentValue.DoubleValue = &value
		}
	}

	values[group] = currentValue
	return nil
}

func (self *MaxAggregator) ColumnName() string {
	return "max"
}

func (self *MaxAggregator) ColumnType() protocol.FieldDefinition_Type {
	return self.fieldType
}

func (self *MaxAggregator) GetValue(series string, group interface{}) *protocol.FieldValue {
	value := self.values[series][group]
	return &value
}

func (self *MaxAggregator) InitializeFieldsMetadata(series *protocol.Series) error {
	for idx, field := range series.Fields {
		if *field.Name == self.fieldName {
			self.fieldIndex = idx
			self.fieldType = *field.Type

			switch self.fieldType {
			case protocol.FieldDefinition_INT32,
				protocol.FieldDefinition_INT64,
				protocol.FieldDefinition_DOUBLE:
				// that's fine
			default:
				return common.NewQueryError(common.InvalidArgument, fmt.Sprintf("Field %s has invalid type %v", self.fieldName, self.fieldType))
			}

			return nil
		}
	}

	return common.NewQueryError(common.InvalidArgument, fmt.Sprintf("Unknown column name %s", self.fieldName))
}

func NewMaxAggregator(_ *parser.Query, value *parser.Value) (Aggregator, error) {
	if len(value.Elems) != 1 {
		return nil, common.NewQueryError(common.WrongNumberOfArguments, "max take one argument only")
	}

	return &MaxAggregator{
		fieldName: value.Elems[0].Name,
		values:    make(map[string]map[interface{}]protocol.FieldValue),
	}, nil
}

type MinAggregator struct {
	fieldName  string
	fieldIndex int
	fieldType  protocol.FieldDefinition_Type
	values     map[string]map[interface{}]protocol.FieldValue
}

func (self *MinAggregator) AggregatePoint(series string, group interface{}, p *protocol.Point) error {
	values := self.values[series]
	if values == nil {
		values = make(map[interface{}]protocol.FieldValue)
		self.values[series] = values
	}

	currentValue := values[group]

	switch self.fieldType {
	case protocol.FieldDefinition_INT64:
		if value := *p.Values[self.fieldIndex].Int64Value; currentValue.Int64Value == nil || *currentValue.Int64Value > value {
			currentValue.Int64Value = &value
		}
	case protocol.FieldDefinition_INT32:
		if value := *p.Values[self.fieldIndex].IntValue; currentValue.IntValue == nil || *currentValue.IntValue > value {
			currentValue.IntValue = &value
		}
	case protocol.FieldDefinition_DOUBLE:
		if value := *p.Values[self.fieldIndex].DoubleValue; currentValue.DoubleValue == nil || *currentValue.DoubleValue > value {
			currentValue.DoubleValue = &value
		}
	}

	values[group] = currentValue
	return nil
}

func (self *MinAggregator) ColumnName() string {
	return "min"
}

func (self *MinAggregator) ColumnType() protocol.FieldDefinition_Type {
	return self.fieldType
}

func (self *MinAggregator) GetValue(series string, group interface{}) *protocol.FieldValue {
	value := self.values[series][group]
	return &value
}

func (self *MinAggregator) InitializeFieldsMetadata(series *protocol.Series) error {
	for idx, field := range series.Fields {
		if *field.Name == self.fieldName {
			self.fieldIndex = idx
			self.fieldType = *field.Type

			switch self.fieldType {
			case protocol.FieldDefinition_INT32,
				protocol.FieldDefinition_INT64,
				protocol.FieldDefinition_DOUBLE:
				// that's fine
			default:
				return common.NewQueryError(common.InvalidArgument, fmt.Sprintf("Field %s has invalid type %v", self.fieldName, self.fieldType))
			}

			return nil
		}
	}

	return common.NewQueryError(common.InvalidArgument, fmt.Sprintf("Unknown column name %s", self.fieldName))
}

func NewMinAggregator(_ *parser.Query, value *parser.Value) (Aggregator, error) {
	if len(value.Elems) != 1 {
		return nil, common.NewQueryError(common.WrongNumberOfArguments, "max take one argument only")
	}

	return &MinAggregator{
		fieldName: value.Elems[0].Name,
		values:    make(map[string]map[interface{}]protocol.FieldValue),
	}, nil
}
