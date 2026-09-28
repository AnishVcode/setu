package value

import (
	"fmt"
	"time"
)

// Date is a civil calendar date with no time and no zone.
//
// Statutory dates ("the notification takes effect on 12 June 2019") are civil
// dates, not instants. Storing them as time.Time invites a timezone to shift a
// commencement date across a boundary and change which version of the law
// applies. Spec 4.4 requires that legal deadlines be computed only against a
// jurisdiction's official calendar; this type keeps the date itself free of
// any zone so that computation is the only place a zone is ever considered.
type Date struct {
	Year  int
	Month int // 1-12
	Day   int // 1-31
}

// MaxDate is the open end of a validity interval — a rule with no `until`.
var MaxDate = Date{Year: 9999, Month: 12, Day: 31}

// MinDate is the open start of a validity interval.
var MinDate = Date{Year: 1, Month: 1, Day: 1}

// NewDate validates and constructs a civil date.
func NewDate(y, m, d int) (Date, error) {
	if m < 1 || m > 12 {
		return Date{}, fmt.Errorf("invalid month %d", m)
	}
	if d < 1 || d > daysInMonth(y, m) {
		return Date{}, fmt.Errorf("invalid day %d for %04d-%02d", d, y, m)
	}
	return Date{Year: y, Month: m, Day: d}, nil
}

// ParseDate reads the YYYY-MM-DD form used by the rule language.
func ParseDate(s string) (Date, error) {
	var y, m, d int
	if _, err := fmt.Sscanf(s, "%04d-%02d-%02d", &y, &m, &d); err != nil {
		return Date{}, fmt.Errorf("date must be YYYY-MM-DD, got %q", s)
	}
	if len(s) != 10 {
		return Date{}, fmt.Errorf("date must be YYYY-MM-DD, got %q", s)
	}
	return NewDate(y, m, d)
}

func (d Date) String() string { return fmt.Sprintf("%04d-%02d-%02d", d.Year, d.Month, d.Day) }

// IsZero reports whether the date was never set.
func (d Date) IsZero() bool { return d.Year == 0 && d.Month == 0 && d.Day == 0 }

// Value lifts the date into a runtime value.
func (d Date) Value() Value { return OfDate(d) }

// Compare orders two dates: -1, 0 or +1.
func (d Date) Compare(o Date) int {
	if d.Year != o.Year {
		return sign(d.Year - o.Year)
	}
	if d.Month != o.Month {
		return sign(d.Month - o.Month)
	}
	return sign(d.Day - o.Day)
}

func (d Date) Before(o Date) bool { return d.Compare(o) < 0 }
func (d Date) After(o Date) bool  { return d.Compare(o) > 0 }
func (d Date) Equal(o Date) bool  { return d.Compare(o) == 0 }

// InInterval reports whether d falls in [from, until), the half-open interval
// every effective-dated object in the graph carries. Half-open is deliberate:
// a rule superseded on 1 April and its successor commencing on 1 April must not
// both apply on that day.
func (d Date) InInterval(from, until Date) bool {
	if !from.IsZero() && d.Before(from) {
		return false
	}
	if !until.IsZero() && !d.Before(until) {
		return false
	}
	return true
}

// AddDays returns the date d days later, using the proleptic Gregorian calendar.
func (d Date) AddDays(n int) Date {
	t := time.Date(d.Year, time.Month(d.Month), d.Day, 0, 0, 0, 0, time.UTC).AddDate(0, 0, n)
	return Date{Year: t.Year(), Month: int(t.Month()), Day: t.Day()}
}

// AddPeriod applies a calendar period. Months are added before days, and a day
// that overflows the target month is clamped to the month's last day: one year
// from 29 February 2024 is 28 February 2025, not 1 March. Licence validity
// periods are stated in calendar terms and must behave this way.
func (d Date) AddPeriod(p Period) Date {
	y := d.Year + p.Years
	m := d.Month + p.Months
	for m > 12 {
		m -= 12
		y++
	}
	for m < 1 {
		m += 12
		y--
	}
	day := d.Day
	if max := daysInMonth(y, m); day > max {
		day = max
	}
	return Date{Year: y, Month: m, Day: day}.AddDays(p.Days)
}

// DaysBetween returns the number of days from d to o, negative when o precedes d.
func (d Date) DaysBetween(o Date) int {
	a := time.Date(d.Year, time.Month(d.Month), d.Day, 0, 0, 0, 0, time.UTC)
	b := time.Date(o.Year, time.Month(o.Month), o.Day, 0, 0, 0, 0, time.UTC)
	return int(b.Sub(a).Hours() / 24)
}

func daysInMonth(y, m int) int {
	return time.Date(y, time.Month(m)+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	}
	return 0
}

// Period is a calendar duration: "1 year", "60 days", "6 months".
//
// A period is not a quantity of days. "Valid for 1 year" and "valid for 365
// days" differ across a leap year, and licences have been litigated over less.
// Periods therefore never convert to a number and only ever apply to a Date.
type Period struct {
	Years  int
	Months int
	Days   int
}

func (p Period) String() string {
	switch {
	case p.Years != 0 && p.Months == 0 && p.Days == 0:
		return plural(p.Years, "year")
	case p.Months != 0 && p.Years == 0 && p.Days == 0:
		return plural(p.Months, "month")
	case p.Days != 0 && p.Years == 0 && p.Months == 0:
		return plural(p.Days, "day")
	}
	s := ""
	if p.Years != 0 {
		s += plural(p.Years, "year") + " "
	}
	if p.Months != 0 {
		s += plural(p.Months, "month") + " "
	}
	if p.Days != 0 {
		s += plural(p.Days, "day")
	}
	if s == "" {
		return "0 days"
	}
	return trimTrailingSpace(s)
}

// IsZero reports whether the period is empty.
func (p Period) IsZero() bool { return p.Years == 0 && p.Months == 0 && p.Days == 0 }

// ApproxDays gives a nominal length for sorting and forecasting only.
// It is never used to compute a legal deadline.
func (p Period) ApproxDays() int { return p.Years*365 + p.Months*30 + p.Days }

func plural(n int, w string) string {
	if n == 1 || n == -1 {
		return fmt.Sprintf("%d %s", n, w)
	}
	return fmt.Sprintf("%d %ss", n, w)
}

func trimTrailingSpace(s string) string {
	for len(s) > 0 && s[len(s)-1] == ' ' {
		s = s[:len(s)-1]
	}
	return s
}
