// Copyright 2014, The Serviced Authors. All rights reserved.
// Use of this source code is governed by the Apache 2.0
// license that can be found in the LICENSE file.

package api

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	elastigo "github.com/zenoss/elastigo/api"
	"github.com/zenoss/elastigo/core"
	"github.com/zenoss/glog"
	"github.com/control-center/serviced/domain/service"
)

// ExportLogs exports logs from ElasticSearch.
// serviceIds: list of services to select (includes their children). Empty slice means no filter
// from: yyyy.mm.dd (inclusive), "" means unbounded
// to: yyyy.mm.dd (inclusive), "" means unbounded
// outfile: the exported logs will tgz'd and written here. "" means "./serviced-log-export.tgz".
func (a *api) ExportLogs(serviceIds []string, from, to, outfile string) (err error) {
	var e error
	files := []*os.File{}
	fileIndex := make(map[string]map[string]int) // host => filename => index

	// make sure we can write to outfile
	if outfile == "" {
		pwd, e := os.Getwd()
		if e != nil {
			return fmt.Errorf("could not determine current directory: %s", e)
		}
		outfile = filepath.Join(pwd, "serviced-log-export.tgz")
	}
	fp, e := filepath.Abs(outfile)
	if e != nil {
		return fmt.Errorf("could not convert '%s' to an absolute path: %v", outfile, e)
	}
	outfile = filepath.Clean(fp)
	tgzfile, e := os.Create(outfile)
	if e != nil {
		return fmt.Errorf("could not create %s: %s", outfile, e)
	}
	tgzfile.Close()
	if e = os.Remove(outfile); e != nil {
		return fmt.Errorf("could not remove %s: %s", outfile, e)
	}

	// Validate and normalize the date range filter attributes "from" and "to"
	if from == "" && to == "" {
		to = time.Now().UTC().Format("2006.01.02")
		from = time.Now().UTC().AddDate(0, 0, -1).Format("2006.01.02")
	}
	if from != "" {
		if from, e = NormalizeYYYYMMDD(from); e != nil {
			return e
		}
	}
	if to != "" {
		if to, e = NormalizeYYYYMMDD(to); e != nil {
			return e
		}
	}

	query := "*"
	if len(serviceIds) > 0 {
		services, e := a.GetServices()
		if e != nil {
			return e
		}
		serviceMap := make(map[string]*service.Service)
		for _, service := range services {
			serviceMap[service.ID] = service
		}
		serviceIDMap := make(map[string]bool) //includes serviceIds, and their children as well
		for _, serviceID := range serviceIds {
			serviceIDMap[serviceID] = true
		}
		for _, service := range services {
			srvc := service
			for {
				found := false
				for _, serviceID := range serviceIds {
					if srvc.ID == serviceID {
						serviceIDMap[service.ID] = true
						found = true
						break
					}
				}
				if found || srvc.ParentServiceID == "" {
					break
				}
				srvc = serviceMap[srvc.ParentServiceID]
			}
		}
		re := regexp.MustCompile("\\A[\\w\\-]+\\z") //only letters, numbers, underscores, and dashes
		queryParts := []string{}
		for serviceID := range serviceIDMap {
			if re.FindStringIndex(serviceID) == nil {
				return fmt.Errorf("invalid service ID format: %s", serviceID)
			}
			queryParts = append(queryParts, fmt.Sprintf("\"%s\"", strings.Replace(serviceID, "-", "\\-", -1)))
		}
		query = fmt.Sprintf("service:(%s)", strings.Join(queryParts, " OR "))
	}

	// Get a temporary directory
	tempdir, e := ioutil.TempDir("", "serviced-log-export-")
	if e != nil {
		return fmt.Errorf("could not create temp directory: %s", e)
	}
	defer os.RemoveAll(tempdir)

	days, e := LogstashDays()
	if e != nil {
		return e
	}
	foundIndexedDay := false
	for _, yyyymmdd := range days {
		// Skip the indexes that are filtered out by the date range
		if (from != "" && yyyymmdd < from) || (to != "" && yyyymmdd > to) {
			continue
		} else {
			foundIndexedDay = true
		}

		logstashIndex := fmt.Sprintf("logstash-%s", yyyymmdd)
		result, e := core.SearchUri(logstashIndex, "", query, "1m", 1000)
		if e != nil {
			return fmt.Errorf("failed to search elasticsearch: %s", e)
		}
		//TODO: Submit a patch to elastigo to support the "clear scroll" api. Add a "defer" here.
		remaining := result.Hits.Total > 0
		for remaining {
			result, e = core.Scroll(false, result.ScrollId, "1m")
			hits := result.Hits.Hits
			total := len(hits)
			for i := 0; i < total; i++ {
				host, logfile, compactLines, e := parseLogSource(hits[i].Source)
				if e != nil {
					return e
				}
				if _, found := fileIndex[host]; !found {
					fileIndex[host] = make(map[string]int)
				}
				if _, found := fileIndex[host][logfile]; !found {
					index := len(files)
					filename := filepath.Join(tempdir, fmt.Sprintf("%03d.log", index))
					file, e := os.Create(filename)
					if e != nil {
						return fmt.Errorf("failed to create file %s: %s", filename, e)
					}
					defer func() {
						if e := file.Close(); e != nil && err == nil {
							err = fmt.Errorf("failed to close file '%s' cleanly: %s", filename, e)
						}
					}()
					fileIndex[host][logfile] = index
					files = append(files, file)
				}
				index := fileIndex[host][logfile]
				file := files[index]
				filename := filepath.Join(tempdir, fmt.Sprintf("%03d.log", index))
				for _, line := range compactLines {
					formatted := fmt.Sprintf("%016x\t%016x\t%s\n", line.Timestamp, line.Offset, line.Message)
					if _, e := file.WriteString(formatted); e != nil {
						return fmt.Errorf("failed writing to file %s: %s", filename, e)
					}
				}
			}
			remaining = len(hits) > 0
		}
	}
	if !foundIndexedDay {
		return fmt.Errorf("no logstash indexes exist for the given date range %s - %s", from, to)
	}

	indexData := []string{}
	for host, logfileIndex := range fileIndex {
		for logfile, i := range logfileIndex {
			filename := filepath.Join(tempdir, fmt.Sprintf("%03d.log", i))
			tmpfilename := filepath.Join(tempdir, fmt.Sprintf("%03d.log.tmp", i))
			cmd := exec.Command("sort", filename, "-uo", tmpfilename)
			if output, e := cmd.CombinedOutput(); e != nil {
				return fmt.Errorf("failed sorting %s, error: %v, output: %s", filename, e, output)
			}
			cmd = exec.Command("mv", tmpfilename, filename)
			if output, e := cmd.CombinedOutput(); e != nil {
				return fmt.Errorf("failed moving %s %s, error: %v, output: %s", tmpfilename, filename, e, output)
			}
			cmd = exec.Command("sed", "s/^[0-9a-f]*\\t[0-9a-f]*\\t//", "-i", filename)
			if output, e := cmd.CombinedOutput(); e != nil {
				return fmt.Errorf("failed stripping sort prefixes from %s, error: %v, output: %s", filename, e, output)
			}
			indexData = append(indexData, fmt.Sprintf("%03d.log\t%s\t%s", i, strconv.Quote(host), strconv.Quote(logfile)))
		}
	}
	sort.Strings(indexData)
	indexData = append([]string{"INDEX OF LOG FILES", "File\tHost\tOriginal Filename"}, indexData...)
	indexData = append(indexData, "")
	indexFile := filepath.Join(tempdir, "index.txt")
	e = ioutil.WriteFile(indexFile, []byte(strings.Join(indexData, "\n")), 0644)
	if e != nil {
		return fmt.Errorf("failed writing to %s: %s", indexFile, e)
	}

	cmd := exec.Command("tar", "-czf", outfile, "-C", filepath.Dir(tempdir), filepath.Base(tempdir))
	if output, e := cmd.CombinedOutput(); e != nil {
		return fmt.Errorf("failed to write tgz cmd:%+v, error:%v, output:%s", cmd, e, string(output))
	}
	return nil
}

type logSingleLine struct {
	Host      string    `json:"host"`
	File      string    `json:"file"`
	Timestamp time.Time `json:"@timestamp"`
	Offset    string    `json:"offset"`
	Message   string    `json:"message"`
}

type logMultiLine struct {
	Host      string    `json:"host"`
	File      string    `json:"file"`
	Timestamp time.Time `json:"@timestamp"`
	Offset    []string  `json:"offset"`
	Message   string    `json:"message"`
}

type compactLogLine struct {
	Timestamp int64 //nanoseconds since the epoch, truncated at the minute to hide jitter
	Offset    uint64
	Message   string
}

var newline = regexp.MustCompile("\\r?\\n")

// convertOffsets converts a list of strings into a list of uint64s
func convertOffsets(offsets []string) ([]uint64, error) {
	result := make([]uint64, len(offsets))
	for i, offsetString := range offsets {
		offset, e := strconv.ParseUint(offsetString, 10, 64)
		if e != nil {
			return result, fmt.Errorf("failed to parse offset[%d] \"%s\" in \"%s\": %s", i, offsetString, offsets, e)
		}
		result[i] = offset
	}

	return result, nil
}

// uint64sAreSorted returns true if input values are sorted in increasing order - mimics sort.IntsAreSorted()
func uint64sAreSorted(values []uint64) bool {
	if len(values) == 0 {
		return true
	}

	previousValue := values[0]
	for _, value := range values {
		if value < previousValue {
			return false
		}
		previousValue = value
	}
	return true
}

// getMinValue returns the minimum value in an array of uint64
func getMinValue(values []uint64) uint64 {
	result := uint64(math.MaxUint64)
	for _, value := range values {
		if value < result {
			result = value
		}
	}
	return result
}

// generateOffsets uses the minimum offset in the array as a base returns an array of offsets where
// each offset is the base + index
func generateOffsets(messages []string, offsets []uint64) []uint64 {
	result := make([]uint64, len(messages))
	minOffset := getMinValue(offsets)
	if minOffset == uint64(math.MaxUint64) {
		minOffset = 0
	}
	for i, _ := range result {
		result[i] = minOffset + uint64(i)
	}
	return result
}

// return: host, file, lines, error
func parseLogSource(source []byte) (string, string, []compactLogLine, error) {
	// attempt to unmarshal into singleLine
	var line logSingleLine
	if e := json.Unmarshal(source, &line); e == nil {
		offset := uint64(0)
		if len(line.Offset) != 0 {
			var e error
			offset, e = strconv.ParseUint(line.Offset, 10, 64)
			if e != nil {
				return "", "", nil, fmt.Errorf("failed to parse offset \"%s\" in \"%s\": %s", line.Offset, source, e)
			}
		}
		compactLine := compactLogLine{
			Timestamp: truncateToMinute(line.Timestamp.UnixNano()),
			Offset:    offset,
			Message:   line.Message,
		}
		return line.Host, line.File, []compactLogLine{compactLine}, nil
	}

	// attempt to unmarshal into multiLine
	var multiLine logMultiLine
	if e := json.Unmarshal(source, &multiLine); e != nil {
		return "", "", nil, fmt.Errorf("failed to parse JSON \"%s\": %s", source, e)
	}

	// build offsets - list of uint64
	offsets, e := convertOffsets(multiLine.Offset)
	if e != nil {
		return "", "", nil, fmt.Errorf("failed to parse JSON \"%s\": %s", source, e)
	}

	// verify number of lines in message against number of offsets
	messages := newline.Split(multiLine.Message, -1)
	if len(offsets)+1 == len(messages) {
		glog.Warningf("number of offsets for %s:%s (numLines:%d numOffsets:%d) is one less than number of lines: %s", multiLine.Host, multiLine.File, len(messages), len(offsets), source)
		numLines := len(messages)
		if numLines > 1 {
			lastOffset := uint64(len(messages[numLines-2])) + offsets[numLines-1]
			offsets = append(offsets, lastOffset)
		}
	} else if len(offsets) > len(messages) {
		glog.Warningf("number of offsets for %s:%s (numLines:%d numOffsets:%d) is greater than number of lines: %s", multiLine.Host, multiLine.File, len(messages), len(multiLine.Offset), source)
	} else if len(offsets) < len(messages) {
		glog.Warningf("number of offsets for %s:%s (numLines:%d numOffsets:%d) is less than number of lines: %s", multiLine.Host, multiLine.File, len(messages), len(multiLine.Offset), source)
		offsets = generateOffsets(messages, offsets)
		glog.Warningf("new offsets: %v", offsets)
	}

	// deal with offsets that are not sorted in increasing order
	if !uint64sAreSorted(offsets) {
		glog.Warningf("offsets are not sorted: %s", offsets)
		offsets = generateOffsets(messages, offsets)
		glog.Warningf("new offsets: %v", offsets)
	}

	// build compactLines
	timestamp := truncateToMinute(multiLine.Timestamp.UnixNano())
	compactLines := make([]compactLogLine, len(messages))
	for i, offset := range offsets {
		compactLines = append(compactLines, compactLogLine{
			Timestamp: timestamp,
			Offset:    offset,
			Message:   messages[i],
		})
	}
	return multiLine.Host, multiLine.File, compactLines, nil
}

// NormalizeYYYYMMDD matches optional non-digits, 4 digits, optional non-digits,
// 2 digits, optional non-digits, 2 digits, optional non-digits
// Returns those 8 digits formatted as "dddd.dd.dd", or error if unparseable.
func NormalizeYYYYMMDD(s string) (string, error) {
	match := yyyymmddMatcher.FindStringSubmatch(s)
	if match == nil {
		return "", fmt.Errorf("could not parse '%s' as yyyymmdd", s)
	}
	return fmt.Sprintf("%s.%s.%s", match[1], match[2], match[3]), nil
}

var yyyymmddMatcher = regexp.MustCompile("\\A[^0-9]*([0-9]{4})[^0-9]*([0-9]{2})[^0-9]*([0-9]{2})[^0-9]*\\z")

// Returns a list of all the dates with a logstash-YYYY.MM.DD index available in ElasticSearch.
// The strings are in YYYY.MM.DD format, and in reverse chronological order.
var LogstashDays = func() ([]string, error) {
	response, e := elastigo.DoCommand("GET", "/_aliases", nil)
	if e != nil {
		return []string{}, fmt.Errorf("couldn't fetch list of indices: %s", e)
	}
	var aliasMap map[string]interface{}
	if e = json.Unmarshal(response, &aliasMap); e != nil {
		return []string{}, fmt.Errorf("couldn't parse response (%s): %s", response, e)
	}
	result := make([]string, 0, len(aliasMap))
	for index := range aliasMap {
		if trimmed := strings.TrimPrefix(index, "logstash-"); trimmed != index {
			if trimmed, e = NormalizeYYYYMMDD(trimmed); e != nil {
				trimmed = ""
			}
			result = append(result, trimmed)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(result)))
	return result, nil
}

func truncateToMinute(nanos int64) int64 {
	return nanos / int64(time.Minute) * int64(time.Minute)
}
