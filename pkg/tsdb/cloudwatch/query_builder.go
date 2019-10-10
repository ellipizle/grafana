package cloudwatch

import (
	"errors"
	"fmt"
	"math/rand"
	"regexp"
	"sort"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/cloudwatch"
	"github.com/grafana/grafana/pkg/components/simplejson"
	"github.com/grafana/grafana/pkg/tsdb"
)

type queryBuilderError struct {
	err   error
	RefID string
}

func (e *queryBuilderError) Error() string {
	return fmt.Sprintf("Error parsing query %s, %s", e.RefID, e.err)
}

func (e *CloudWatchExecutor) buildQueriesByRegion(queryContext *tsdb.TsdbQuery) (map[string]map[string]*CloudWatchQuery, error) {
	metricQueriesByRegion := make(map[string]map[string]*CloudWatchQuery)

	for i, model := range queryContext.Queries {
		queryType := model.Model.Get("type").MustString()
		if queryType != "timeSeriesQuery" && queryType != "" {
			continue
		}

		RefID := queryContext.Queries[i].RefId
		query, err := parseQuery(queryContext.Queries[i].Model, RefID)
		if err != nil {
			return nil, &queryBuilderError{err, RefID}
		}
		if _, ok := metricQueriesByRegion[query.Region]; !ok {
			metricQueriesByRegion[query.Region] = make(map[string]*CloudWatchQuery)
		}
		metricQueriesByRegion[query.Region][RefID] = query
	}

	return metricQueriesByRegion, nil
}

func parseQuery(model *simplejson.Json, refId string) (*CloudWatchQuery, error) {
	region, err := model.Get("region").String()
	if err != nil {
		return nil, err
	}

	namespace, err := model.Get("namespace").String()
	if err != nil {
		return nil, err
	}

	metricName, err := model.Get("metricName").String()
	if err != nil {
		return nil, err
	}

	id := model.Get("id").MustString("")

	expression := model.Get("expression").MustString("")

	dimensions, err := parseDimensions(model)
	if err != nil {
		return nil, err
	}

	statistics, err := parseStatistics(model)
	if err != nil {
		return nil, err
	}

	identifier := id
	if identifier == "" || len(statistics) > 1 {
		identifier = generateUniqueString()
	}

	p := model.Get("period").MustString("")
	if p == "" {
		if namespace == "AWS/EC2" {
			p = "300"
		} else {
			p = "60"
		}
	}

	var period int
	if regexp.MustCompile(`^\d+$`).Match([]byte(p)) {
		period, err = strconv.Atoi(p)
		if err != nil {
			return nil, err
		}
	} else {
		d, err := time.ParseDuration(p)
		if err != nil {
			return nil, err
		}
		period = int(d.Seconds())
	}

	alias := model.Get("alias").MustString()

	returnData := !model.Get("hide").MustBool(false)
	queryType := model.Get("type").MustString()
	if queryType == "" {
		// If no type is provided we assume we are called by alerting service, which requires to return data!
		// Note, this is sort of a hack, but the official Grafana interfaces do not carry the information
		// who (which service) called the TsdbQueryEndpoint.Query(...) function.
		returnData = true
	}
	highResolution := model.Get("highResolution").MustBool(false)

	return &CloudWatchQuery{
		RefId:          refId,
		Region:         region,
		Namespace:      namespace,
		MetricName:     metricName,
		Dimensions:     dimensions,
		Statistics:     aws.StringSlice(statistics),
		Period:         period,
		Alias:          alias,
		Id:             id,
		Identifier:     identifier,
		Expression:     expression,
		ReturnData:     returnData,
		HighResolution: highResolution,
	}, nil
}

func parseStatisticsAndExtendedStatistics(model *simplejson.Json) ([]string, []string, error) {
	var statistics []string
	var extendedStatistics []string

	for _, s := range model.Get("statistics").MustArray() {
		if ss, ok := s.(string); ok {
			if _, isStandard := standardStatistics[ss]; isStandard {
				statistics = append(statistics, ss)
			} else {
				extendedStatistics = append(extendedStatistics, ss)
			}
		} else {
			return nil, nil, errors.New("failed to parse")
		}
	}

	return statistics, extendedStatistics, nil
}

func parseStatistics(model *simplejson.Json) ([]string, error) {
	var statistics []string

	for _, s := range model.Get("statistics").MustArray() {
		statistics = append(statistics, s.(string))
	}

	return statistics, nil
}

func parseDimensions(model *simplejson.Json) ([]*cloudwatch.Dimension, error) {
	var result []*cloudwatch.Dimension

	for k, v := range model.Get("dimensions").MustMap() {
		kk := k
		if vv, ok := v.(string); ok {
			result = append(result, &cloudwatch.Dimension{
				Name:  &kk,
				Value: &vv,
			})
		} else {
			return nil, errors.New("failed to parse")
		}
	}

	sort.Slice(result, func(i, j int) bool {
		return *result[i].Name < *result[j].Name
	})
	return result, nil
}

func generateUniqueString() string {
	var letter = []rune("abcdefghijklmnopqrstuvwxyz")

	b := make([]rune, 8)
	for i := range b {
		b[i] = letter[rand.Intn(len(letter))]
	}
	return string(b)
}