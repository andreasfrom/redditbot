package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/smtp"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	commentHeaderFormat = "Top example usage of *%s* from [Wordnik](http://www.wordnik.com/):\n\n"
	commentBodyFormat   = ">%s\n\n[Origin.](%s)\n\n"
	commentFooter       = "*I'm a bot and unaffiliated with Wordnik.*"

	userAgent   = ""
	contentType = "application/x-www-form-urlencoded"

	timeBetweenRequests = 2 * time.Second
	timeBetweenComments = 10 * time.Minute
	timeCached          = 30 * time.Second

	redditUsername = ""
	redditPassword = ""
	wordnikKey     = ""

	emailAddress  = ""
	emailPassword = ""
	emailServer   = ""
	emailPort     = ""
	emailSubject  = "Subject: RedditBot: An error occured!\n"
	emailMime     = "MIME-version: 1.0;\nContent-Type: text/plain; charset=\"UTF-8\";\n\n"
)

var (
	client      = &http.Client{}
	lastRequest time.Time
)

type User struct {
	Cookie  string
	Modhash string
}

type UserData struct {
	Data User
}

type UserJson struct {
	Json UserData
}

type JsonData struct {
	Data Entries
}

type Entries struct {
	Children []Entry
}

type Entry struct {
	Data EntryData
}

type EntryData struct {
	Name  string
	Title string
}

type WordnikExample struct {
	Url  string
	Text string
}

func main() {
	user, err := login(redditUsername, redditPassword)
	if err != nil {
		sendErrorEmail(err)
		log.Fatal(err)
	}

	fmt.Printf("Cookie: %s\nModhash: %s\n\n", user.Cookie, user.Modhash)

	before := ""
	limit := 1

	for {
		startTime := time.Now()

		section, err := getSection("new", user, before, limit, "rising")
		if err != nil {
			sendErrorEmail(err)
			log.Fatal(err)
		}

		entries, err := makeEntries(section)
		if err != nil {
			sendErrorEmail(err)
			log.Fatal(err)
		}

		if len(entries) > 0 {
			for i := range entries {
				data := entries[i].Data

				commentText, err := makeComment(data.Title)
				if err != nil {
					sendErrorEmail(err)
					log.Fatal(err)
				}
				if len(commentText) > 0 {
					fmt.Println(data.Name)
					fmt.Println(commentText)
					err = comment(data.Name, commentText, user)
					if err != nil {
						sendErrorEmail(err)
						log.Fatal(err)
					}
					time.Sleep(timeBetweenComments)
					break
				} else {
					fmt.Println("no usable words in: " + data.Title)
				}
			}
		}

		limit = 100
		time.Sleep(timeCached - time.Since(startTime))
	}
}

func login(username string, passwd string) (User, error) {
	var user User

	myUrl := "https://ssl.reddit.com/api/login/" + username
	form := url.Values{"user": {username}, "passwd": {passwd}, "api_type": {"json"}}

	data, err := redditRequest("POST", myUrl, form, make(http.Header))

	if err != nil {
		return user, err
	}

	var jsonObject UserJson
	err = json.Unmarshal(data, &jsonObject)

	if err != nil {
		return user, err
	}

	user = jsonObject.Json.Data
	if len(user.Modhash) == 0 {
		return user, errors.New("login: " + string(data))
	}
	user.Cookie = "reddit_session=" + user.Cookie

	return user, nil
}

func getSection(section string, user User, before string, limit int, sort string) ([]byte, error) {
	myUrl := "http://www.reddit.com/" + section + ".json?sort=" + sort + "&limit=" + strconv.Itoa(limit)

	if before != "" {
		myUrl += "&before=" + before
	}

	header := make(http.Header)
	header.Set("Cookie", user.Cookie)

	data, err := redditRequest("GET", myUrl, url.Values{}, header)
	if err != nil {
		return nil, err
	}

	return data, nil
}

func makeEntries(jsonData []byte) ([]Entry, error) {
	var data JsonData
	err := json.Unmarshal(jsonData, &data)
	if err != nil {
		return data.Data.Children, err
	}

	return data.Data.Children, nil
}

func makeComment(title string) (string, error) {
	var example WordnikExample

	commentHeader := ""
	commentBody := ""

	r, err := regexp.Compile("[^A-Za-z ]")
	if err != nil {
		return "", err
	}

	title = r.ReplaceAllString(title, " ")
	words := strings.Split(title, " ")
	for i := range words {
		words[i] = strconv.Itoa(len(words[i])) + words[i]
	}
	sort.Strings(words)
	for i := range words {
		words[i] = r.ReplaceAllString(words[i], "")
	}

	word := ""
	for i := range words {
		word = words[len(words)-i-1]
		if len(word) > 0 {
			example, err = getExample(word)
			if err != nil {
				return "", err
			}
			if len(example.Text) > 0 {
				break
			}
		}
	}

	if len(example.Text) == 0 {
		return "", nil
	}

	commentHeader = fmt.Sprintf(commentHeaderFormat, word)
	commentBody = fmt.Sprintf(commentBodyFormat, example.Text, example.Url)

	return commentHeader + commentBody + commentFooter, nil
}

func getExample(word string) (WordnikExample, error) {
	var example WordnikExample
	url := "http://api.wordnik.com//v4/word.json/" + word + "/topExample?useCanonical=false"

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return example, err
	}

	req.Header.Add("api_key", wordnikKey)
	resp, err := client.Do(req)
	if err != nil {
		return example, err
	}

	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return example, err
	}

	err = json.Unmarshal(body, &example)
	if err != nil {
		return example, err
	}

	return example, nil
}

func comment(thing string, text string, user User) error {
	form := url.Values{"thing_id": {thing}, "text": {text}, "uh": {user.Modhash}}
	header := make(http.Header)
	header.Set("Cookie", user.Cookie)

	data, err := redditRequest("POST", "http://www.reddit.com/api/comment", form, header)
	if err != nil {
		return err
	}

	if strings.Contains(string(data), "a bot") != true {
		return errors.New("comment: " + string(data))
	}

	return nil
}

func redditRequest(method string, url string, values url.Values, header http.Header) ([]byte, error) {
	form := strings.NewReader(values.Encode())

	req, err := http.NewRequest(method, url, form)
	if err != nil {
		return nil, err
	}

	header.Set("User-Agent", userAgent)
	header.Set("Content-Type", contentType)
	req.Header = header

	time.Sleep(timeBetweenRequests - time.Since(lastRequest))
	lastRequest = time.Now()
	fmt.Printf("Made request: %v\n", lastRequest)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()
	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	return data, nil
}

func sendErrorEmail(errorBody error) {
	auth := smtp.PlainAuth("", emailAddress, emailPassword, emailServer)
	msg := []byte(emailSubject + emailMime + time.Now().String() + "\n\n" + errorBody.Error())
	err := smtp.SendMail(emailServer+":"+emailPort, auth, emailAddress, []string{emailAddress}, msg)
	if err != nil {
		log.Fatal(err)
	}
}
