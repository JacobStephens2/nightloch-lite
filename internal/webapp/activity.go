package webapp

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	githubauth "github.com/JacobStephens2/nightloch-lite/internal/auth"
	"github.com/JacobStephens2/nightloch-lite/internal/partnership"
)

// activityDateLayout is the one date format the filter reads and writes. It is
// a plain calendar day in UTC, which is what the browser's own date input
// submits, and every instant the page renders is labelled beside it (S3-27).
const activityDateLayout = "2006-01-02"

// activityData is one page of one Protectee's chronology as one viewer is
// authorized to read it. The screen serves two framings — a Protectee reading
// their complete history, and a Partner reading the window that opened when
// they accepted — and asks the view which one it is holding rather than
// deciding for itself, because the audience rule belongs to the module (S3-14).
type activityData struct {
	User    githubauth.User
	View    partnership.ActivityView
	Version string
	// BasePath is this chronology's own path, so the filter form and the
	// older-entries link stay on the page the viewer is reading rather than
	// sending a Partner to their own Activity.
	BasePath string
	// Categories is the filter vocabulary, which comes from the module so a
	// screen cannot offer a category the read path does not know.
	Categories []partnership.ActivityCategory
	// From and To are the submitted date bounds echoed back into the form.
	From, To string
	// OlderURL is the next page, and is empty on the last one.
	OlderURL string
	// ClearURL drops every filter without leaving the chronology.
	ClearURL string
	// FilterError says a submitted filter could not be applied, shown above
	// the unfiltered page it fell back to. A filter Nightloch cannot honour is
	// never answered with an empty list, which would read as a claim that
	// nothing happened.
	FilterError string
}

// ownActivity is the signed-in User's own complete chronology, which they keep
// for as long as their User exists (S3-14).
func (a *App) ownActivity(w http.ResponseWriter, r *http.Request) {
	user, ok := a.currentUser(w, r)
	if !ok {
		return
	}
	a.renderActivity(w, r, user, user.ID, "/activity")
}

// protecteeActivity is one Protectee's chronology read by a Partner. The
// module decides whether this viewer holds a window at all, so a former
// Partner, a pending invitee, and a stranger all reach the same unavailable
// answer as an id that names nobody.
func (a *App) protecteeActivity(w http.ResponseWriter, r *http.Request) {
	user, ok := a.currentUser(w, r)
	if !ok {
		return
	}
	protecteeID, valid := pathID(r)
	if !valid {
		a.renderActivityUnavailable(w, r)
		return
	}
	a.renderActivity(w, r, user, protecteeID, fmt.Sprintf("/activity/%d", protecteeID))
}

// activityQuery reads the filter and page out of the query string. Every part
// is optional, and a malformed one is reported rather than dropped: a viewer
// who mistypes a date should be told their filter was not applied, not handed
// a page that looks filtered and is not.
func activityQuery(values url.Values) (partnership.ActivityQuery, error) {
	query := partnership.ActivityQuery{
		Category: partnership.ActivityCategory(values.Get("category")),
		Actor:    values.Get("actor"),
	}
	if raw := values.Get("from"); raw != "" {
		from, err := time.Parse(activityDateLayout, raw)
		if err != nil {
			return partnership.ActivityQuery{}, partnership.ErrActivityFilterInvalid
		}
		query.From = from.UTC()
	}
	if raw := values.Get("to"); raw != "" {
		to, err := time.Parse(activityDateLayout, raw)
		if err != nil {
			return partnership.ActivityQuery{}, partnership.ErrActivityFilterInvalid
		}
		// A date names a whole day, so the range includes everything that
		// happened on it rather than only its first instant.
		query.To = to.UTC().Add(24*time.Hour - time.Second)
	}
	cursor, err := partnership.ParseActivityCursor(values.Get("after"))
	if err != nil {
		return partnership.ActivityQuery{}, err
	}
	query.After = cursor
	return query, nil
}

// activityURL rebuilds this page's own address with one page position, which
// is how the older-entries link keeps the filter the viewer is reading under.
func activityURL(basePath string, values url.Values, after partnership.ActivityCursor) string {
	next := url.Values{}
	for _, name := range []string{"category", "actor", "from", "to"} {
		if value := values.Get(name); value != "" {
			next.Set(name, value)
		}
	}
	if !after.Zero() {
		next.Set("after", after.String())
	}
	if len(next) == 0 {
		return basePath
	}
	return basePath + "?" + next.Encode()
}

func (a *App) renderActivity(w http.ResponseWriter, r *http.Request, user githubauth.User, protecteeID int64, basePath string) {
	values := r.URL.Query()
	query, queryErr := activityQuery(values)
	view, err := a.partnerships.Activity(r.Context(), protecteeID, user.ID, query)
	if queryErr == nil && errors.Is(err, partnership.ErrActivityFilterInvalid) {
		queryErr = err
	}
	if queryErr != nil {
		// Authorization is the module's answer and outranks the filter: a
		// viewer who may not read this chronology learns nothing about their
		// filter either.
		view, err = a.partnerships.Activity(r.Context(), protecteeID, user.ID, partnership.ActivityQuery{})
		values = url.Values{}
	}
	if errors.Is(err, partnership.ErrActivityUnavailable) {
		a.renderActivityUnavailable(w, r)
		return
	}
	if err != nil {
		a.internalError(w, r, err)
		return
	}

	data := activityData{
		User:       user,
		View:       view,
		Version:    a.version,
		BasePath:   basePath,
		Categories: partnership.ActivityCategories(),
		From:       values.Get("from"),
		To:         values.Get("to"),
		ClearURL:   basePath,
	}
	if queryErr != nil {
		data.FilterError = "Nightloch could not apply that filter, so this is the newest unfiltered page."
	}
	if view.More() {
		data.OlderURL = activityURL(basePath, values, view.NextPage)
	}
	a.render(w, r, http.StatusOK, "activity", data)
}

func (a *App) renderActivityUnavailable(w http.ResponseWriter, r *http.Request) {
	a.render(w, r, http.StatusNotFound, "activity-unavailable", pageData{Version: a.version})
}
