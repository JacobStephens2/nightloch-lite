package webapp

import (
	"errors"
	"net/http"

	githubauth "github.com/JacobStephens2/nightloch-lite/internal/auth"
	"github.com/JacobStephens2/nightloch-lite/internal/partnership"
)

// activityData is one Protectee's chronology as one viewer is authorized to
// read it. Own separates the two framings the same screen serves: a Protectee
// reading their complete history, and a Partner reading the window that opened
// when they accepted.
type activityData struct {
	User    githubauth.User
	View    partnership.ActivityView
	Own     bool
	Version string
}

// ownActivity is the signed-in User's own complete chronology, which they keep
// for as long as their User exists (S3-14).
func (a *App) ownActivity(w http.ResponseWriter, r *http.Request) {
	user, ok := a.currentUser(w, r)
	if !ok {
		return
	}
	a.renderActivity(w, r, user, user.ID)
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
	a.renderActivity(w, r, user, protecteeID)
}

func (a *App) renderActivity(w http.ResponseWriter, r *http.Request, user githubauth.User, protecteeID int64) {
	view, err := a.partnerships.Activity(r.Context(), protecteeID, user.ID)
	if errors.Is(err, partnership.ErrActivityUnavailable) {
		a.renderActivityUnavailable(w, r)
		return
	}
	if err != nil {
		a.internalError(w, r, err)
		return
	}
	a.render(w, r, http.StatusOK, "activity", activityData{
		User: user, View: view, Own: protecteeID == user.ID, Version: a.version,
	})
}

func (a *App) renderActivityUnavailable(w http.ResponseWriter, r *http.Request) {
	a.render(w, r, http.StatusNotFound, "activity-unavailable", pageData{Version: a.version})
}
