package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/gravitl/netmaker/logger"
	"github.com/gravitl/netmaker/logic"
	"github.com/gravitl/netmaker/models"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/github"
	"io"
	"net/http"
)

var github_functions = map[string]interface{}{
	init_provider:   initGithub,
	get_user_info:   getGithubUserInfo,
	handle_callback: handleGithubCallback,
	handle_login:    handleGithubLogin,
	verify_user:     verifyGithubUser,
}

// == handle github authentication here ==

func initGithub(redirectURL string, clientID string, clientSecret string) {
	auth_provider = &oauth2.Config{
		RedirectURL:  redirectURL,
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Scopes:       []string{},
		Endpoint:     github.Endpoint,
	}
}

func handleGithubLogin(w http.ResponseWriter, r *http.Request) {
	var oauth_state_string = logic.RandomString(user_signin_length)
	if auth_provider == nil {
		handleOauthNotConfigured(w)
		return
	}

	if err := logic.SetState(oauth_state_string); err != nil {
		handleOauthNotConfigured(w)
		return
	}

	var url = auth_provider.AuthCodeURL(oauth_state_string)
	http.Redirect(w, r, url, http.StatusTemporaryRedirect)
}

func handleGithubCallback(w http.ResponseWriter, r *http.Request) {

	var rState, rCode = getStateAndCode(r)
	var content, err = getGithubUserInfo(rState, rCode)
	if err != nil {
		logger.Log(1, "error when getting user info from github:", err.Error())
		handleOauthNotConfigured(w)
		return
	}
	username := content.Login

	user, err := logic.GetUser(username)
	if err != nil { // user must not exist, so try to make one
		if err = addUser(username, true); err != nil {
			return
		}
		user, err = logic.GetUser(username)
	}
	if err != nil {
		handleOauthUserNotFound(w)
		return
	}
	if !(user.IsSuperAdmin || user.IsAdmin) {
		handleOauthUserNotAllowed(w)
		return
	}
	var newPass, fetchErr = fetchPassValue("")
	if fetchErr != nil {
		return
	}
	// send a netmaker jwt token
	var authRequest = models.UserAuthParams{
		UserName: username,
		Password: newPass,
	}

	var jwt, jwtErr = logic.VerifyAuthRequest(authRequest)
	if jwtErr != nil {
		logger.Log(1, "could not parse jwt for user", authRequest.UserName, "due to error", jwtErr.Error())
		return
	}
	performSSORedirect("GitHub", w, r, jwt, username)
}

func getGithubUserInfo(state string, code string) (*OAuthUser, error) {
	oauth_state_string, isValid := logic.IsStateValid(state)
	if (!isValid || state != oauth_state_string) && !isStateCached(state) {
		return nil, fmt.Errorf("invalid oauth state")
	}
	var token, err = auth_provider.Exchange(context.Background(), code)
	if err != nil {
		return nil, fmt.Errorf("code exchange failed: %s", err.Error())
	}
	if !token.Valid() {
		return nil, fmt.Errorf("GitHub code exchange yielded invalid token")
	}
	var data []byte
	data, err = json.Marshal(token)
	if err != nil {
		return nil, fmt.Errorf("failed to convert token to json: %s", err.Error())
	}
	var httpClient = &http.Client{}
	var httpReq, reqErr = http.NewRequest("GET", "https://api.github.com/user", nil)
	if reqErr != nil {
		return nil, fmt.Errorf("failed to create request to GitHub")
	}
	httpReq.Header.Set("Authorization", "token "+token.AccessToken)
	response, err := httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed getting user info: %s", err.Error())
	}
	defer response.Body.Close()
	contents, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("failed reading response body: %s", err.Error())
	}
	var userInfo = &OAuthUser{}
	if err = json.Unmarshal(contents, userInfo); err != nil {
		return nil, fmt.Errorf("failed parsing email from response data: %s", err.Error())
	}
	userInfo.AccessToken = string(data)
	return userInfo, nil
}

func verifyGithubUser(token *oauth2.Token) bool {
	return token.Valid()
}
