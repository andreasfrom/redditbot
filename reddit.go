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
	"strconv"
	"strings"
	"time"
)

const (
	timeFormat                = "Jan 02, 2006"
	commentHeaderSingleFormat = "/u/%s's most popular post:\n\n"
	commentHeaderPluralFormat = "/u/%s's %d most popular posts:\n\n"
	tableBodyFormat           = "%d | [%s](%s) | [%d](%s) | %s | /r/%s\n"
	tableHeader               = "Score | Title | #Cmts | Posted | Sub\n--:|:--|--:|:--|:--\n"
	commentFooter             = "*This bot was made by /u/AUTHOR, please address any complaints or suggestions to him.*"
	firstPost                 = "This is OP's first post. Congratulations OP!\n\n"

	userAgent   = "/u/BOTNAME by /u/AUTHOR"
	contentType = "application/x-www-form-urlencoded"

	timeBetweenRequests = 2 * time.Second
	timeCached          = 30 * time.Second //10 * time.Minute

	numberOfAuthorPosts = 10

	redditUsername = ""
	redditPassword = ""

	emailAddress  = ""
	emailPassword = ""
	emailServer   = "smtp.gmail.com"
	emailPort     = "587"
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
	Data Entries "data"
}

type Entries struct {
	Children []Entry "children"
}

type Entry struct {
	Data EntryData "data"
}

type EntryData struct {
	Url          string
	Permalink    string
	Subreddit    string
	Name         string
	Author       string
	Title        string
	Score        int
	Num_comments int
	Created_UTC  float32
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

	for true {
		startTime := time.Now()

		section, err := getSection("new", user, before, limit)
		if err != nil {
			sendErrorEmail(err)
			log.Fatal(err)
		}

		entries, err := makeEntries(section)
		if err != nil {
			sendErrorEmail(err)
			log.Fatal(err)
		}

		entriesLength := len(entries)

		if entriesLength > 0 {
			for i := 0; i < entriesLength; i++ {
				author := entries[i].Data.Author
				authorEntries, err := getAuthorPosts(author, "top", numberOfAuthorPosts, user)
				if err != nil {
					sendErrorEmail(err)
					log.Fatal(err)
				}

				/*
				name := entries[i].Data.Name
				fmt.Println(name)
				*/
				commentText := makeComment(authorEntries)
				fmt.Println(commentText)
				//err = comment(name, commentText, user)
				if err != nil {
					sendErrorEmail(err)
					log.Fatal(err)
				}
			}

			before = entries[0].Data.Name
		}

		limit = 100
		time.Sleep(timeCached - time.Since(startTime))
	}
}

func login(username string, passwd string) (User, error) {
	var user User

	myUrl := "https://ssl.reddit.com/api/login/" + username
	form := url.Values{"user": {username}, "passwd": {passwd}, "api_type": {"json"}}

	data, err := customRequest("POST", myUrl, form, make(http.Header))

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

func getSection(section string, user User, before string, limit int) ([]byte, error) {
	myUrl := "http://www.reddit.com/" + section + ".json?sort=rising&limit=" + strconv.Itoa(limit)

	if before != "" {
		myUrl += "&before=" + before
	}

	header := make(http.Header)
	header.Set("Cookie", user.Cookie)

	data, err := customRequest("GET", myUrl, url.Values{}, header)
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

func getAuthorPosts(author string, sort string, limit int, user User) ([]Entry, error) {
	var data JsonData
	myUrl := "http://www.reddit.com/user/" + author + "/submitted.json?limit=" + strconv.Itoa(limit) + "&sort=" + sort

	header := make(http.Header)
	header.Set("Cookie", user.Cookie)

	jsonData, err := customRequest("GET", myUrl, url.Values{}, header)
	if err != nil {
		return data.Data.Children, err
	}

	err = json.Unmarshal(jsonData, &data)
	if err != nil {
		return data.Data.Children, err
	}

	return data.Data.Children, nil
}

func makeComment(entries []Entry) string {
	length := len(entries)
	commentHeader := ""
	if length == 0 {
		return firstPost + commentFooter
	} else if length == 1 {
		commentHeader = fmt.Sprintf(commentHeaderSingleFormat, entries[0].Data.Author)
	} else {
		commentHeader = fmt.Sprintf(commentHeaderPluralFormat, entries[0].Data.Author, length)
	}
	tableBody := ""

	for i := 0; i < length; i++ {
		data := entries[i].Data
		posted := time.Unix(int64(data.Created_UTC), 0).Format(timeFormat)
		title := strings.TrimSpace(strings.Replace(data.Title, "|", "&#124;", -1))

		tableBody += fmt.Sprintf(tableBodyFormat,
			data.Score, title, data.Url, data.Num_comments, data.Permalink, posted, data.Subreddit)
	}

	return commentHeader + tableHeader + tableBody + commentFooter
}

func comment(thing string, text string, user User) error {
	form := url.Values{"thing_id": {thing}, "text": {text}, "uh": {user.Modhash}}
	header := make(http.Header)
	header.Set("Cookie", user.Cookie)

	data, err := customRequest("POST", "http://www.reddit.com/api/comment", form, header)
	if err != nil {
		return err
	}

	if strings.Contains(string(data), "/u/andreasfrom") != true {
		return errors.New("comment: " + string(data))
	}

	return nil
}

func customRequest(method string, url string, values url.Values, header http.Header) ([]byte, error) {
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
	//fmt.Printf("Made request: %v\n", lastRequest)
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
