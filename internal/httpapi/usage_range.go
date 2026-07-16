package httpapi

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

const maxCalendarRangeDays = 366

func calendarWindowFromRequest(r *http.Request, now time.Time) (time.Time, time.Time, bool, error) {
	fromValue := strings.TrimSpace(r.URL.Query().Get("from"))
	toValue := strings.TrimSpace(r.URL.Query().Get("to"))
	if fromValue == "" && toValue == "" {
		return time.Time{}, time.Time{}, false, nil
	}
	if fromValue == "" || toValue == "" {
		return time.Time{}, time.Time{}, false, fmt.Errorf("from and to must be provided together")
	}

	location := time.Local
	if timezone := strings.TrimSpace(r.URL.Query().Get("tz")); timezone != "" {
		var err error
		location, err = time.LoadLocation(timezone)
		if err != nil {
			return time.Time{}, time.Time{}, false, fmt.Errorf("invalid timezone %q", timezone)
		}
	}
	start, err := time.ParseInLocation("2006-01-02", fromValue, location)
	if err != nil {
		return time.Time{}, time.Time{}, false, fmt.Errorf("invalid from date %q", fromValue)
	}
	through, err := time.ParseInLocation("2006-01-02", toValue, location)
	if err != nil {
		return time.Time{}, time.Time{}, false, fmt.Errorf("invalid to date %q", toValue)
	}
	if through.Before(start) {
		return time.Time{}, time.Time{}, false, fmt.Errorf("to must not be before from")
	}
	today := now.In(location)
	today = time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, location)
	if through.After(today) {
		return time.Time{}, time.Time{}, false, fmt.Errorf("to must not be in the future")
	}
	endExclusive := through.AddDate(0, 0, 1)
	days := 0
	for cursor := start; cursor.Before(endExclusive); cursor = cursor.AddDate(0, 0, 1) {
		days++
		if days > maxCalendarRangeDays {
			return time.Time{}, time.Time{}, false, fmt.Errorf("date range must not exceed %d days", maxCalendarRangeDays)
		}
	}
	return start, endExclusive, true, nil
}
