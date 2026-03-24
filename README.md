# lolzteam-go

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![CI](https://github.com/kyo-lzt/lolzteam-go/actions/workflows/ci.yml/badge.svg)](https://github.com/kyo-lzt/lolzteam-go/actions)

Go SDK для [Lolzteam](https://lolz.live) Forum и Market API. **266 эндпоинтов** (151 Forum + 115 Market), автоматически сгенерированные из OpenAPI спецификаций. **Ноль внешних зависимостей** -- только стандартная библиотека Go.

---

## Содержание / Table of Contents

- [Быстрый старт / Quick Start](#быстрый-старт--quick-start)
- [Опции клиента / Client Options](#опции-клиента--client-options)
- [Прокси / Proxy](#прокси--proxy)
- [Авто-retry / Auto-retry](#авто-retry--auto-retry)
- [Обработка ошибок / Error Handling](#обработка-ошибок--error-handling)
- [Rate Limits](#rate-limits)
- [Forum API](#forum-api)
  - [OAuth](#oauth)
  - [Ассеты / Assets](#ассеты--assets)
  - [Категории / Categories](#категории--categories)
  - [Форумы / Forums](#форумы--forums)
  - [Ссылки / Links](#ссылки--links)
  - [Страницы / Pages](#страницы--pages)
  - [Навигация / Navigation](#навигация--navigation)
  - [Темы / Threads](#темы--threads)
  - [Посты / Posts](#посты--posts)
  - [Пользователи / Users](#пользователи--users)
  - [Посты профиля / Profile Posts](#посты-профиля--profile-posts)
  - [Личные сообщения / Conversations](#личные-сообщения--conversations)
  - [Уведомления / Notifications](#уведомления--notifications)
  - [Теги / Tags](#теги--tags)
  - [Поиск / Search](#поиск--search)
  - [Batch](#batch)
  - [Чатбокс / Chatbox](#чатбокс--chatbox)
  - [Формы / Forms](#формы--forms)
- [Market API](#market-api)
  - [Категории / Category](#категории--category)
  - [Список / List](#список--list)
  - [Управление / Managing](#управление--managing)
  - [Профиль / Profile](#профиль--profile)
  - [Корзина / Cart](#корзина--cart)
  - [Покупка / Purchasing](#покупка--purchasing)
  - [Кастомные скидки / Custom Discounts](#кастомные-скидки--custom-discounts)
  - [Публикация / Publishing](#публикация--publishing)
  - [Платежи / Payments](#платежи--payments)
  - [Автоплатежи / Auto Payments](#автоплатежи--auto-payments)
  - [Прокси / Proxy (Market)](#прокси--proxy-market)
  - [IMAP](#imap)
  - [Batch (Market)](#batch-market)
- [Генерация кода / Code Generation](#генерация-кода--code-generation)
- [Сборка и тесты / Build & Test](#сборка-и-тесты--build--test)
- [Структура проекта / Project Structure](#структура-проекта--project-structure)
- [Лицензия / License](#лицензия--license)

---

## Быстрый старт / Quick Start

```bash
git clone https://github.com/kyo-lzt/lolzteam-go.git
cd lolzteam-go
go build ./...
```

Требуется **Go 1.23+**.

```go
package main

import (
	"context"
	"fmt"

	"github.com/kyo-lzt/lolzteam-go/forum"
	"github.com/kyo-lzt/lolzteam-go/market"
)

func main() {
	ctx := context.Background()

	// Быстрый старт — достаточно передать токен
	forumClient, _ := forum.NewClientFromToken("your_token")
	marketClient, _ := market.NewClientFromToken("your_token")

	threads, _ := forumClient.Threads.List(ctx, nil)
	fmt.Println(threads)

	items, _ := marketClient.Category.All(ctx, nil)
	fmt.Println(items)
}
```

---

## Опции клиента / Client Options

Все поля кроме `Token` опциональны и имеют разумные значения по умолчанию.

```go
config := lolzteam.Config{
	Token:   "your_token",
	BaseURL: "https://prod-api.lolz.live",
	Timeout: 30 * time.Second,
	Proxy:   &lolzteam.ProxyConfig{URL: "socks5://127.0.0.1:1080"},
	Retry: &lolzteam.RetryConfig{
		MaxRetries: 5,
		BaseDelay:  time.Second,
		MaxDelay:   30 * time.Second,
	},
	RateLimit: &lolzteam.RateLimitConfig{
		RequestsPerMinute:       200,
		SearchRequestsPerMinute: 30,
	},
	OnRetry: func(info lolzteam.RetryInfo) {
		fmt.Printf("Retry #%d for %s %s\n", info.Attempt, info.Method, info.Path)
	},
}
```

| Field | Type | Default | Description |
|---|---|---|---|
| `Token` | `string` | *required* | API access token |
| `BaseURL` | `string` | per-client | Forum: `https://prod-api.lolz.live`, Market: `https://prod-api.lzt.market` |
| `Timeout` | `time.Duration` | `30s` | Таймаут запроса |
| `Proxy` | `*ProxyConfig` | `nil` | Прокси конфигурация |
| `Retry` | `*RetryConfig` | `DefaultRetryConfig()` | Настройки retry (включено по умолчанию) |
| `RateLimit` | `*RateLimitConfig` | per-client | Rate limiter конфигурация |
| `OnRetry` | `func(RetryInfo)` | `nil` | Колбэк при каждом retry |

---

## Прокси / Proxy

Передайте URL прокси в конфигурации. Поддерживаемые схемы: `http`, `https`, `socks5`.

```go
// HTTP-прокси
config := lolzteam.Config{
	Token: "your_token",
	Proxy: &lolzteam.ProxyConfig{URL: "http://127.0.0.1:8080"},
}

// Прокси с авторизацией
config = lolzteam.Config{
	Token: "your_token",
	Proxy: &lolzteam.ProxyConfig{URL: "http://user:pass@proxy.example.com:3128"},
}

// SOCKS5-прокси
config = lolzteam.Config{
	Token: "your_token",
	Proxy: &lolzteam.ProxyConfig{URL: "socks5://127.0.0.1:1080"},
}
```

---

## Авто-retry / Auto-retry

Неудачные запросы автоматически повторяются при транзиентных ошибках. Задержка рассчитывается по формуле экспоненциального отступа с jitter. Заголовок `Retry-After` на 429-ответах учитывается.

| Status | Retried | Behavior |
|--------|---------|----------|
| 429 | Yes | Использует `Retry-After` заголовок |
| 500 | No | Возвращается немедленно |
| 502, 503, 504 | Yes | Exponential backoff с jitter |
| Network errors | Yes | Timeout и connection errors |
| 401, 403 | No | Возвращается немедленно |
| 404 | No | Возвращается немедленно |
| Other | No | Возвращается немедленно |

Формула задержки: `min(baseDelay * 2^attempt + random(0, baseDelay), maxDelay)`

```go
// Retry включён по умолчанию (DefaultRetryConfig)
// Достаточно просто создать клиент:
forumClient, _ := forum.NewClientFromToken("...")

// Отключить retry
config := lolzteam.Config{
	Token: "...",
	Retry: nil,
}

// Кастомный retry + OnRetry колбэк
config = lolzteam.Config{
	Token: "...",
	Retry: &lolzteam.RetryConfig{
		MaxRetries: 5,
		BaseDelay:  time.Second,
		MaxDelay:   30 * time.Second,
	},
	OnRetry: func(info lolzteam.RetryInfo) {
		fmt.Printf("Retry #%d\n", info.Attempt)
	},
}
```

---

## Обработка ошибок / Error Handling

Все ошибки принадлежат типизированной иерархии. Используйте `errors.As` для сопоставления:

```go
var rateLimitErr *lolzteam.RateLimitError
if errors.As(err, &rateLimitErr) {
	fmt.Printf("Rate limit, повтор через %s\n", rateLimitErr.RetryAfter)
}

var httpErr *lolzteam.HttpError
if errors.As(err, &httpErr) {
	fmt.Printf("HTTP %d: %s\n", httpErr.StatusCode, httpErr.Body)
}

var authErr *lolzteam.AuthError
if errors.As(err, &authErr) {
	fmt.Println("Невалидный или истекший токен")
}

var notFoundErr *lolzteam.NotFoundError
if errors.As(err, &notFoundErr) {
	fmt.Println("Не найдено")
}

var networkErr *lolzteam.NetworkError
if errors.As(err, &networkErr) {
	fmt.Println("Сетевая ошибка:", networkErr.Unwrap())
}
```

Иерархия ошибок:

```
LolzteamError
├── HttpError
│   ├── RateLimitError    (429)
│   ├── AuthError         (401, 403)
│   ├── NotFoundError     (404)
│   └── ServerError       (5xx)
├── NetworkError
├── ConfigError
└── RetryExhaustedError
```

---

## Rate Limits

Встроенный rate limiter использует алгоритм token bucket. Mutex-based, thread-safe. Когда токены заканчиваются, запросы блокируются -- ни один запрос не отбрасывается.

| Client | Default limit |
|--------|---------------|
| Forum  | 300 req/min   |
| Market | 120 req/min   |
| Market (search) | 20 req/min |

```go
config := lolzteam.Config{
	Token: "...",
	RateLimit: &lolzteam.RateLimitConfig{
		RequestsPerMinute:       200,
		SearchRequestsPerMinute: 30,
	},
}
```

---

## Forum API

Группы API: `Assets`, `Batch`, `Categories`, `Chatbox`, `Conversations`, `Forms`, `Forums`, `Links`, `Navigation`, `Notifications`, `OAuth`, `Pages`, `Posts`, `ProfilePosts`, `Search`, `Tags`, `Threads`, `Users`.

Все методы принимают `context.Context` первым аргументом.

### OAuth

```go
// Получить OAuth-токен (POST /oauth/token)
token, err := forumClient.OAuth.Token(ctx, forum.OAuthTokenBody{GrantType: "authorization_code", ClientID: "...", ClientSecret: "..."})
```

### Ассеты / Assets

```go
// Получить CSS-ассеты (GET /assets/css)
css, err := forumClient.Assets.CSS(ctx, nil)
```

### Категории / Categories

```go
// Получить список категорий (GET /categories)
categories, err := forumClient.Categories.List(ctx, nil)

// Получить категорию по ID (GET /categories/:id)
category, err := forumClient.Categories.Get(ctx, 1)
```

### Форумы / Forums

```go
// Получить список форумов (GET /forums)
forums, err := forumClient.Forums.List(ctx, nil)

// Получить сгруппированные форумы (GET /forums/grouped)
grouped, err := forumClient.Forums.Grouped(ctx)

// Получить форум по ID (GET /forums/:id)
f, err := forumClient.Forums.Get(ctx, 876)

// Получить подписчиков форума (GET /forums/:id/followers)
followers, err := forumClient.Forums.Followers(ctx, 876)

// Подписаться на форум (POST /forums/:id/followers)
follow, err := forumClient.Forums.Follow(ctx, 876, nil)

// Отписаться от форума (DELETE /forums/:id/followers)
unfollow, err := forumClient.Forums.Unfollow(ctx, 876)

// Получить форумы, на которые подписан (GET /forums/followed)
followed, err := forumClient.Forums.Followed(ctx, nil)

// Получить настройки ленты (GET /forums/feed-options)
feedOptions, err := forumClient.Forums.GetFeedOptions(ctx)

// Редактировать настройки ленты (PUT /forums/feed-options)
editFeed, err := forumClient.Forums.EditFeedOptions(ctx, nil)
```

### Ссылки / Links

```go
// Получить список ссылок (GET /links)
links, err := forumClient.Links.List(ctx)

// Получить ссылку по ID (GET /links/:id)
link, err := forumClient.Links.Get(ctx, 1)
```

### Страницы / Pages

```go
// Получить список страниц (GET /pages)
pages, err := forumClient.Pages.List(ctx, nil)

// Получить страницу по ID (GET /pages/:id)
page, err := forumClient.Pages.Get(ctx, 1)
```

### Навигация / Navigation

```go
// Получить элементы навигации (GET /navigation)
nav, err := forumClient.Navigation.List(ctx, nil)
```

### Темы / Threads

```go
// Получить список тем (GET /threads)
threads, err := forumClient.Threads.List(ctx, &forum.ThreadsListParams{ForumID: intPtr(876)})

// Создать тему (POST /threads)
thread, err := forumClient.Threads.Create(ctx, &forum.ThreadsCreateBody{ForumID: 876, PostBody: "Текст", Title: "Заголовок"})

// Создать конкурс (POST /threads/contests)
contest, err := forumClient.Threads.CreateContest(ctx, &forum.ThreadsCreateContestBody{ForumID: 876, PostBody: "Текст", Title: "Конкурс"})

// Забрать тему (POST /threads/claim)
claim, err := forumClient.Threads.Claim(ctx, nil)

// Получить тему по ID (GET /threads/:id)
t, err := forumClient.Threads.Get(ctx, 123, nil)

// Редактировать тему (PUT /threads/:id)
edit, err := forumClient.Threads.Edit(ctx, 123, nil)

// Удалить тему (DELETE /threads/:id)
del, err := forumClient.Threads.Delete(ctx, 123, nil)

// Переместить тему (POST /threads/:id/move)
move, err := forumClient.Threads.Move(ctx, 123, &forum.ThreadsMoveBody{ForumID: 877})

// Поднять тему (POST /threads/:id/bump)
bump, err := forumClient.Threads.Bump(ctx, 123)

// Скрыть тему (POST /threads/:id/hide)
hide, err := forumClient.Threads.Hide(ctx, 123)

// Добавить в избранное (POST /threads/:id/star)
star, err := forumClient.Threads.Star(ctx, 123)

// Убрать из избранного (DELETE /threads/:id/star)
unstar, err := forumClient.Threads.Unstar(ctx, 123)

// Получить подписчиков темы (GET /threads/:id/followers)
tFollowers, err := forumClient.Threads.Followers(ctx, 123)

// Подписаться на тему (POST /threads/:id/followers)
tFollow, err := forumClient.Threads.Follow(ctx, 123, nil)

// Отписаться от темы (DELETE /threads/:id/followers)
tUnfollow, err := forumClient.Threads.Unfollow(ctx, 123)

// Получить темы, на которые подписан (GET /threads/followed)
tFollowed, err := forumClient.Threads.Followed(ctx, nil)

// Навигация темы (GET /threads/:id/navigation)
tNav, err := forumClient.Threads.Navigation(ctx, 123)

// Получить опрос (GET /threads/:id/poll)
poll, err := forumClient.Threads.PollGet(ctx, 123)

// Проголосовать в опросе (POST /threads/:id/poll/votes)
vote, err := forumClient.Threads.PollVote(ctx, 123, nil)

// Непрочитанные темы (GET /threads/unread)
unread, err := forumClient.Threads.Unread(ctx, nil)

// Недавние темы (GET /threads/recent)
recent, err := forumClient.Threads.Recent(ctx, nil)

// Завершить тему (POST /threads/:id/finish)
finish, err := forumClient.Threads.Finish(ctx, 123)
```

### Посты / Posts

```go
// Получить список постов (GET /posts)
posts, err := forumClient.Posts.List(ctx, &forum.PostsListParams{ThreadID: intPtr(123)})

// Создать пост (POST /posts)
post, err := forumClient.Posts.Create(ctx, &forum.PostsCreateBody{ThreadID: 123, PostBody: "Текст поста"})

// Получить пост по ID (GET /posts/:id)
p, err := forumClient.Posts.Get(ctx, 456)

// Редактировать пост (PUT /posts/:id)
editPost, err := forumClient.Posts.Edit(ctx, 456, nil)

// Удалить пост (DELETE /posts/:id)
delPost, err := forumClient.Posts.Delete(ctx, 456, nil)

// Получить лайки поста (GET /posts/:id/likes)
likes, err := forumClient.Posts.Likes(ctx, 456, nil)

// Поставить лайк (POST /posts/:id/likes)
like, err := forumClient.Posts.Like(ctx, 456)

// Убрать лайк (DELETE /posts/:id/likes)
unlike, err := forumClient.Posts.Unlike(ctx, 456)

// Получить причины жалобы (GET /posts/:id/report-reasons)
reasons, err := forumClient.Posts.ReportReasons(ctx, 456)

// Пожаловаться на пост (POST /posts/:id/report)
report, err := forumClient.Posts.Report(ctx, 456, nil)

// Получить комментарии к посту (GET /posts/comments)
comments, err := forumClient.Posts.CommentsGet(ctx, &forum.PostsCommentsGetParams{PostID: 456})

// Создать комментарий (POST /posts/comments)
comment, err := forumClient.Posts.CommentsCreate(ctx, &forum.PostsCommentsCreateBody{PostID: 456, CommentBody: "Комментарий"})

// Редактировать комментарий (PUT /posts/comments)
editComment, err := forumClient.Posts.CommentsEdit(ctx, &forum.PostsCommentsEditBody{CommentID: 789, CommentBody: "Новый"})

// Удалить комментарий (DELETE /posts/comments)
delComment, err := forumClient.Posts.CommentsDelete(ctx, &forum.PostsCommentsDeleteBody{CommentID: 789})

// Пожаловаться на комментарий (POST /posts/comments/report)
reportComment, err := forumClient.Posts.CommentsReport(ctx, &forum.PostsCommentsReportBody{CommentID: 789})
```

### Пользователи / Users

```go
// Получить список пользователей (GET /users)
users, err := forumClient.Users.List(ctx, nil)

// Получить поля профиля (GET /users/fields)
fields, err := forumClient.Users.Fields(ctx)

// Найти пользователя (GET /users/find)
found, err := forumClient.Users.Find(ctx, &forum.UsersFindParams{Username: strPtr("test")})

// Получить пользователя по ID (GET /users/:id)
user, err := forumClient.Users.Get(ctx, 1, nil)

// Редактировать пользователя (PUT /users/:id)
editUser, err := forumClient.Users.Edit(ctx, 1, nil)

// Получить жалобы пользователя (GET /users/:id/claims)
claims, err := forumClient.Users.Claims(ctx, 1, nil)

// Загрузить аватар (POST /users/:id/avatar)
avatar, err := forumClient.Users.AvatarUpload(ctx, 1, &forum.UsersAvatarUploadBody{})

// Удалить аватар (DELETE /users/:id/avatar)
delAvatar, err := forumClient.Users.AvatarDelete(ctx, 1)

// Обрезать аватар (POST /users/:id/avatar-crop)
cropAvatar, err := forumClient.Users.AvatarCrop(ctx, 1, &forum.UsersAvatarCropBody{})

// Загрузить фон (POST /users/:id/background)
bg, err := forumClient.Users.BackgroundUpload(ctx, 1, &forum.UsersBackgroundUploadBody{})

// Удалить фон (DELETE /users/:id/background)
delBg, err := forumClient.Users.BackgroundDelete(ctx, 1)

// Обрезать фон (POST /users/:id/background-crop)
cropBg, err := forumClient.Users.BackgroundCrop(ctx, 1, &forum.UsersBackgroundCropBody{})

// Получить подписчиков (GET /users/:id/followers)
uFollowers, err := forumClient.Users.Followers(ctx, 1, nil)

// Подписаться (POST /users/:id/followers)
uFollow, err := forumClient.Users.Follow(ctx, 1)

// Отписаться (DELETE /users/:id/followers)
uUnfollow, err := forumClient.Users.Unfollow(ctx, 1)

// Получить подписки (GET /users/:id/followings)
followings, err := forumClient.Users.Followings(ctx, 1, nil)

// Получить лайки пользователя (GET /users/:id/likes)
uLikes, err := forumClient.Users.Likes(ctx, 1, nil)

// Получить список игнорируемых (GET /users/ignored)
ignored, err := forumClient.Users.Ignored(ctx, nil)

// Игнорировать пользователя (POST /users/:id/ignore)
ignore, err := forumClient.Users.Ignore(ctx, 1)

// Изменить настройки игнорирования (PUT /users/:id/ignore)
ignoreEdit, err := forumClient.Users.IgnoreEdit(ctx, 1, nil)

// Перестать игнорировать (DELETE /users/:id/ignore)
unignore, err := forumClient.Users.Unignore(ctx, 1)

// Получить контент пользователя (GET /users/:id/contents)
contents, err := forumClient.Users.Contents(ctx, 1, nil)

// Получить трофеи (GET /users/:id/trophies)
trophies, err := forumClient.Users.Trophies(ctx, 1)

// Получить типы секретного ответа (GET /users/secret-answer-types)
saTypes, err := forumClient.Users.SecretAnswerTypes(ctx)

// Сбросить секретный ответ (POST /users/sa-reset)
saReset, err := forumClient.Users.SAReset(ctx)

// Отменить сброс секретного ответа (POST /users/sa-cancel-reset)
saCancelReset, err := forumClient.Users.SACancelReset(ctx)
```

### Посты профиля / Profile Posts

```go
// Получить список постов профиля (GET /profile-posts)
profilePosts, err := forumClient.ProfilePosts.List(ctx, 1, nil)

// Получить пост профиля (GET /profile-posts/:id)
pp, err := forumClient.ProfilePosts.Get(ctx, 100)

// Редактировать пост профиля (PUT /profile-posts/:id)
editPP, err := forumClient.ProfilePosts.Edit(ctx, 100, nil)

// Удалить пост профиля (DELETE /profile-posts/:id)
delPP, err := forumClient.ProfilePosts.Delete(ctx, 100, nil)

// Получить причины жалобы (GET /profile-posts/:id/report-reasons)
ppReasons, err := forumClient.ProfilePosts.ReportReasons(ctx, 100)

// Пожаловаться (POST /profile-posts/:id/report)
ppReport, err := forumClient.ProfilePosts.Report(ctx, 100, nil)

// Создать пост профиля (POST /profile-posts)
createPP, err := forumClient.ProfilePosts.Create(ctx, &forum.ProfilePostsCreateBody{PostBody: "Текст"})

// Закрепить пост профиля (POST /profile-posts/:id/stick)
stickPP, err := forumClient.ProfilePosts.Stick(ctx, 100)

// Открепить пост профиля (POST /profile-posts/:id/unstick)
unstickPP, err := forumClient.ProfilePosts.Unstick(ctx, 100)

// Получить лайки поста профиля (GET /profile-posts/:id/likes)
ppLikes, err := forumClient.ProfilePosts.Likes(ctx, 100)

// Поставить лайк (POST /profile-posts/:id/likes)
ppLike, err := forumClient.ProfilePosts.Like(ctx, 100)

// Убрать лайк (DELETE /profile-posts/:id/likes)
ppUnlike, err := forumClient.ProfilePosts.Unlike(ctx, 100)

// Получить список комментариев (GET /profile-posts/:id/comments)
ppComments, err := forumClient.ProfilePosts.CommentsList(ctx, &forum.ProfilePostsCommentsListParams{ProfilePostID: 100})

// Создать комментарий (POST /profile-posts/:id/comments)
ppComment, err := forumClient.ProfilePosts.CommentsCreate(ctx, &forum.ProfilePostsCommentsCreateBody{ProfilePostID: 100, CommentBody: "Текст"})

// Редактировать комментарий (PUT /profile-posts/comments/:id)
ppEditComment, err := forumClient.ProfilePosts.CommentsEdit(ctx, &forum.ProfilePostsCommentsEditBody{CommentID: 200, CommentBody: "Новый"})

// Удалить комментарий (DELETE /profile-posts/comments/:id)
ppDelComment, err := forumClient.ProfilePosts.CommentsDelete(ctx, &forum.ProfilePostsCommentsDeleteBody{CommentID: 200})

// Получить комментарий (GET /profile-posts/:id/comments/:id)
ppGetComment, err := forumClient.ProfilePosts.CommentsGet(ctx, 100, 200)

// Пожаловаться на комментарий (POST /profile-posts/comments/:id/report)
ppReportComment, err := forumClient.ProfilePosts.CommentsReport(ctx, 200, nil)
```

### Личные сообщения / Conversations

```go
// Получить список диалогов (GET /conversations)
convs, err := forumClient.Conversations.List(ctx, nil)

// Создать диалог (POST /conversations)
conv, err := forumClient.Conversations.Create(ctx, nil)

// Обновить диалог (PUT /conversations)
updateConv, err := forumClient.Conversations.Update(ctx, &forum.ConversationsUpdateBody{ConversationID: 1, Title: "Новая тема"})

// Удалить диалог (DELETE /conversations)
delConv, err := forumClient.Conversations.Delete(ctx, &forum.ConversationsDeleteBody{ConversationID: 1})

// Начать диалог (POST /conversations/start)
startConv, err := forumClient.Conversations.Start(ctx, &forum.ConversationsStartBody{RecipientID: 1, Title: "Привет"})

// Сохранить диалог (POST /conversations/save)
saveConv, err := forumClient.Conversations.Save(ctx, &forum.ConversationsSaveBody{ConversationID: 1})

// Получить диалог по ID (GET /conversations/:id)
getConv, err := forumClient.Conversations.Get(ctx, 1)

// Получить сообщения диалога (GET /conversations/:id/messages)
msgs, err := forumClient.Conversations.MessagesList(ctx, 1, nil)

// Отправить сообщение (POST /conversations/:id/messages)
msg, err := forumClient.Conversations.MessagesCreate(ctx, 1, &forum.ConversationsMessagesCreateBody{MessageBody: "Текст"})

// Поиск по диалогам (POST /conversations/search)
searchConv, err := forumClient.Conversations.Search(ctx, nil)

// Получить сообщение по ID (GET /conversations/messages/:id)
getMsg, err := forumClient.Conversations.MessagesGet(ctx, 100)

// Редактировать сообщение (PUT /conversations/:id/messages/:id)
editMsg, err := forumClient.Conversations.MessagesEdit(ctx, 1, 100, &forum.ConversationsMessagesEditBody{MessageBody: "Новый"})

// Удалить сообщение (DELETE /conversations/:id/messages/:id)
delMsg, err := forumClient.Conversations.MessagesDelete(ctx, 1, 100)

// Пригласить в диалог (POST /conversations/:id/invite)
invite, err := forumClient.Conversations.Invite(ctx, 1, &forum.ConversationsInviteBody{RecipientID: 2})

// Исключить из диалога (POST /conversations/:id/kick)
kick, err := forumClient.Conversations.Kick(ctx, 1, &forum.ConversationsKickBody{UserID: 2})

// Прочитать диалог (POST /conversations/:id/read)
read, err := forumClient.Conversations.Read(ctx, 1)

// Прочитать все диалоги (POST /conversations/read-all)
readAll, err := forumClient.Conversations.ReadAll(ctx)

// Закрепить сообщение (POST /conversations/:id/messages/:id/stick)
stickMsg, err := forumClient.Conversations.MessagesStick(ctx, 1, 100)

// Открепить сообщение (POST /conversations/:id/messages/:id/unstick)
unstickMsg, err := forumClient.Conversations.MessagesUnstick(ctx, 1, 100)

// Добавить в избранное (POST /conversations/:id/star)
starConv, err := forumClient.Conversations.Star(ctx, 1)

// Убрать из избранного (DELETE /conversations/:id/star)
unstarConv, err := forumClient.Conversations.Unstar(ctx, 1)

// Включить уведомления (POST /conversations/:id/alerts-enable)
alertsOn, err := forumClient.Conversations.AlertsEnable(ctx, 1)

// Отключить уведомления (POST /conversations/:id/alerts-disable)
alertsOff, err := forumClient.Conversations.AlertsDisable(ctx, 1)
```

### Уведомления / Notifications

```go
// Получить список уведомлений (GET /notifications)
notifs, err := forumClient.Notifications.List(ctx, nil)

// Получить уведомление по ID (GET /notifications/:id)
notif, err := forumClient.Notifications.Get(ctx, 1)

// Отметить уведомления прочитанными (POST /notifications/read)
readNotif, err := forumClient.Notifications.Read(ctx, nil)
```

### Теги / Tags

```go
// Получить популярные теги (GET /tags/popular)
popular, err := forumClient.Tags.Popular(ctx)

// Получить список тегов (GET /tags)
tags, err := forumClient.Tags.List(ctx, nil)

// Получить тег по ID (GET /tags/:id)
tag, err := forumClient.Tags.Get(ctx, 1, nil)

// Найти тег (GET /tags/find)
findTag, err := forumClient.Tags.Find(ctx, &forum.TagsFindParams{Tag: "test"})
```

### Поиск / Search

```go
// Поиск по всему (POST /search)
all, err := forumClient.Search.All(ctx, nil)

// Поиск по темам (POST /search/threads)
sThreads, err := forumClient.Search.Threads(ctx, nil)

// Поиск по постам (POST /search/posts)
sPosts, err := forumClient.Search.Posts(ctx, nil)

// Поиск по пользователям (POST /search/users)
sUsers, err := forumClient.Search.Users(ctx, nil)

// Поиск по постам профиля (POST /search/profile-posts)
sPP, err := forumClient.Search.ProfilePosts(ctx, nil)

// Поиск по тегу (POST /search/tagged)
sTagged, err := forumClient.Search.Tagged(ctx, nil)

// Получить результаты поиска (GET /search/results/:id)
results, err := forumClient.Search.Results(ctx, "search_id_123", nil)
```

### Batch

```go
// Выполнить batch-запросы (POST /batch)
batch, err := forumClient.Batch.Execute(ctx, []forum.BatchExecuteItem{{Method: "GET", URI: "/threads"}})
```

### Чатбокс / Chatbox

```go
// Получить индекс чатбокса (GET /chatbox)
chatIndex, err := forumClient.Chatbox.Index(ctx, nil)

// Получить сообщения чатбокса (GET /chatbox/messages)
chatMsgs, err := forumClient.Chatbox.GetMessages(ctx, &forum.ChatboxGetMessagesParams{RoomID: intPtr(1)})

// Отправить сообщение в чатбокс (POST /chatbox/messages)
chatPost, err := forumClient.Chatbox.PostMessage(ctx, &forum.ChatboxPostMessageBody{MessageBody: "Привет"})

// Редактировать сообщение чатбокса (PUT /chatbox/messages)
chatEdit, err := forumClient.Chatbox.EditMessage(ctx, &forum.ChatboxEditMessageBody{MessageID: 1, MessageBody: "Новый текст"})

// Удалить сообщение чатбокса (DELETE /chatbox/messages)
chatDel, err := forumClient.Chatbox.DeleteMessage(ctx, &forum.ChatboxDeleteMessageBody{MessageID: 1})

// Получить онлайн-пользователей (GET /chatbox/online)
chatOnline, err := forumClient.Chatbox.Online(ctx, &forum.ChatboxOnlineParams{RoomID: intPtr(1)})

// Получить причины жалобы (GET /chatbox/report-reasons)
chatReasons, err := forumClient.Chatbox.ReportReasons(ctx, &forum.ChatboxReportReasonsParams{RoomID: intPtr(1)})

// Пожаловаться на сообщение (POST /chatbox/report)
chatReport, err := forumClient.Chatbox.Report(ctx, &forum.ChatboxReportBody{MessageID: 1})

// Получить таблицу лидеров (GET /chatbox/leaderboard)
chatLeader, err := forumClient.Chatbox.GetLeaderboard(ctx, nil)

// Получить список игнорируемых (GET /chatbox/ignore)
chatIgnore, err := forumClient.Chatbox.GetIgnore(ctx)

// Добавить в игнор (POST /chatbox/ignore)
chatAddIgnore, err := forumClient.Chatbox.PostIgnore(ctx, &forum.ChatboxPostIgnoreBody{UserID: 1})

// Удалить из игнора (DELETE /chatbox/ignore)
chatDelIgnore, err := forumClient.Chatbox.DeleteIgnore(ctx, &forum.ChatboxDeleteIgnoreBody{UserID: 1})
```

### Формы / Forms

```go
// Получить список форм (GET /forms)
forms, err := forumClient.Forms.List(ctx, nil)

// Создать форму (POST /forms)
form, err := forumClient.Forms.Create(ctx, forum.FormsCreateBody{Title: "Форма"})
```

---

## Market API

Группы API: `AutoPayments`, `Batch`, `Cart`, `Category`, `CustomDiscounts`, `Imap`, `List`, `Managing`, `Payments`, `Profile`, `Proxy`, `Publishing`, `Purchasing`.

Все методы принимают `context.Context` первым аргументом.

### Категории / Category

```go
// Все аккаунты (GET /category)
all, err := marketClient.Category.All(ctx, nil)

// Список категорий (GET /category)
catList, err := marketClient.Category.List(ctx, nil)

// Параметры категории (GET /:categoryName/params)
catParams, err := marketClient.Category.Params(ctx, "steam")

// Игры категории (GET /:categoryName/games)
catGames, err := marketClient.Category.Games(ctx, "steam")

// Steam-аккаунты (GET /steam)
steam, err := marketClient.Category.Steam(ctx, nil)

// Fortnite-аккаунты (GET /fortnite)
fortnite, err := marketClient.Category.Fortnite(ctx, nil)

// Mihoyo-аккаунты (GET /mihoyo)
mihoyo, err := marketClient.Category.Mihoyo(ctx, nil)

// Riot-аккаунты (GET /riot)
riot, err := marketClient.Category.Riot(ctx, nil)

// Telegram-аккаунты (GET /telegram)
telegram, err := marketClient.Category.Telegram(ctx, nil)

// Supercell-аккаунты (GET /supercell)
supercell, err := marketClient.Category.Supercell(ctx, nil)

// EA-аккаунты (GET /ea)
ea, err := marketClient.Category.EA(ctx, nil)

// World of Tanks аккаунты (GET /world-of-tanks)
wot, err := marketClient.Category.Wot(ctx, nil)

// WoT Blitz аккаунты (GET /wot-blitz)
wotBlitz, err := marketClient.Category.WotBlitz(ctx, nil)

// Подарочные карты (GET /gifts)
gifts, err := marketClient.Category.Gifts(ctx, nil)

// Epic Games аккаунты (GET /epicgames)
epic, err := marketClient.Category.EpicGames(ctx, nil)

// Escape from Tarkov аккаунты (GET /escape-from-tarkov)
eft, err := marketClient.Category.EscapeFromTarkov(ctx, nil)

// Social Club аккаунты (GET /socialclub)
socialClub, err := marketClient.Category.SocialClub(ctx, nil)

// Uplay аккаунты (GET /uplay)
uplay, err := marketClient.Category.Uplay(ctx, nil)

// Discord аккаунты (GET /discord)
discord, err := marketClient.Category.Discord(ctx, nil)

// TikTok аккаунты (GET /tiktok)
tikTok, err := marketClient.Category.TikTok(ctx, nil)

// Instagram аккаунты (GET /instagram)
instagram, err := marketClient.Category.Instagram(ctx, nil)

// Battle.net аккаунты (GET /battlenet)
battleNet, err := marketClient.Category.BattleNet(ctx, nil)

// ChatGPT аккаунты (GET /chatgpt)
chatGPT, err := marketClient.Category.ChatGPT(ctx, nil)

// VPN-аккаунты (GET /vpn)
vpn, err := marketClient.Category.VPN(ctx, nil)

// Roblox аккаунты (GET /roblox)
roblox, err := marketClient.Category.Roblox(ctx, nil)

// Warface аккаунты (GET /warface)
warface, err := marketClient.Category.Warface(ctx, nil)

// Minecraft аккаунты (GET /minecraft)
minecraft, err := marketClient.Category.Minecraft(ctx, nil)

// Hytale аккаунты (GET /hytale)
hytale, err := marketClient.Category.Hytale(ctx, nil)
```

### Список / List

```go
// Получить аккаунты пользователя (GET /user/items)
user, err := marketClient.List.User(ctx, nil)

// Получить заказы (GET /user/orders)
orders, err := marketClient.List.Orders(ctx, nil)

// Получить статусы аккаунтов (GET /user/item-states)
states, err := marketClient.List.States(ctx, nil)

// Скачать аккаунты (GET /user/:type/download)
download, err := marketClient.List.Download(ctx, "items", nil)

// Получить избранное (GET /user/favorites)
favorites, err := marketClient.List.Favorites(ctx, nil)

// Получить просмотренные (GET /user/viewed)
viewed, err := marketClient.List.Viewed(ctx, nil)
```

### Управление / Managing

```go
// Получить аккаунт по ID (GET /items/:id)
item, err := marketClient.Managing.Get(ctx, 123, nil)

// Удалить аккаунт (DELETE /items/:id)
del, err := marketClient.Managing.Delete(ctx, 123, nil)

// Создать жалобу (POST /claims)
claim, err := marketClient.Managing.CreateClaim(ctx, nil)

// Массовое получение (POST /items/bulk-get)
bulk, err := marketClient.Managing.BulkGet(ctx, &market.ManagingBulkGetBody{ItemIDs: []int64{1, 2, 3}})

// Стоимость инвентаря Steam (GET /items/:id/steam-inventory-value)
invValue, err := marketClient.Managing.SteamInventoryValue(ctx, 123, nil)

// Стоимость Steam-аккаунта (GET /steam-value)
steamVal, err := marketClient.Managing.SteamValue(ctx, &market.ManagingSteamValueParams{Link: "https://..."})

// Превью Steam-аккаунта (GET /items/:id/steam-preview)
preview, err := marketClient.Managing.SteamPreview(ctx, 123, nil)

// Редактировать аккаунт (PUT /items/:id)
edit, err := marketClient.Managing.Edit(ctx, 123, nil)

// Получить AI-цену (GET /items/:id/ai-price)
aiPrice, err := marketClient.Managing.AIPrice(ctx, 123)

// Получить авто-цену покупки (GET /items/:id/auto-buy-price)
autoBuyPrice, err := marketClient.Managing.AutoBuyPrice(ctx, 123)

// Добавить заметку (POST /items/:id/note)
note, err := marketClient.Managing.Note(ctx, 123, nil)

// Обновить стоимость Steam (POST /items/:id/steam-update-value)
updateVal, err := marketClient.Managing.SteamUpdateValue(ctx, 123, nil)

// Поднять аккаунт (POST /items/:id/bump)
bump, err := marketClient.Managing.Bump(ctx, 123)

// Включить авто-поднятие (POST /items/:id/auto-bump)
autoBump, err := marketClient.Managing.AutoBump(ctx, 123, nil)

// Отключить авто-поднятие (DELETE /items/:id/auto-bump)
autoBumpOff, err := marketClient.Managing.AutoBumpDisable(ctx, 123)

// Открыть аккаунт (POST /items/:id/open)
open, err := marketClient.Managing.Open(ctx, 123)

// Закрыть аккаунт (POST /items/:id/close)
close, err := marketClient.Managing.Close(ctx, 123)

// Получить изображение (GET /items/:id/image)
image, err := marketClient.Managing.Image(ctx, 123, nil)

// Получить email-код (GET /items/:id/email-code)
emailCode, err := marketClient.Managing.EmailCode(ctx, 123)

// Получить письма (GET /items/letters2)
letters, err := marketClient.Managing.GetLetters2(ctx, nil)

// Получить mafile Steam (GET /items/:id/steam-mafile)
mafile, err := marketClient.Managing.SteamGetMafile(ctx, 123)

// Добавить mafile Steam (POST /items/:id/steam-mafile)
addMafile, err := marketClient.Managing.SteamAddMafile(ctx, 123)

// Удалить mafile Steam (DELETE /items/:id/steam-mafile)
removeMafile, err := marketClient.Managing.SteamRemoveMafile(ctx, 123)

// Получить код mafile Steam (GET /items/:id/steam-mafile-code)
mafileCode, err := marketClient.Managing.SteamMafileCode(ctx, 123)

// Управление Steam SDA (POST /items/:id/steam-sda)
sda, err := marketClient.Managing.SteamSDA(ctx, 123, nil)

// Получить Telegram-код (GET /items/:id/telegram-code)
tgCode, err := marketClient.Managing.TelegramCode(ctx, 123)

// Сбросить авторизацию Telegram (POST /items/:id/telegram-reset-auth)
tgReset, err := marketClient.Managing.TelegramResetAuth(ctx, 123)

// Отказаться от гарантии (POST /items/:id/refuse-guarantee)
refuseG, err := marketClient.Managing.RefuseGuarantee(ctx, 123)

// Отклонить запись видео (POST /items/:id/decline-video-recording)
declineVideo, err := marketClient.Managing.DeclineVideoRecording(ctx, 123, nil)

// Проверить гарантию (GET /items/:id/check-guarantee)
checkG, err := marketClient.Managing.CheckGuarantee(ctx, 123)

// Сменить пароль (POST /items/:id/change-password)
changePwd, err := marketClient.Managing.ChangePassword(ctx, 123, nil)

// Временный пароль email (GET /items/:id/temp-email-password)
tempPwd, err := marketClient.Managing.TempEmailPassword(ctx, 123)

// Добавить приватный тег (POST /items/:id/tag)
tag, err := marketClient.Managing.Tag(ctx, 123, nil)

// Удалить приватный тег (DELETE /items/:id/tag)
untag, err := marketClient.Managing.Untag(ctx, 123, nil)

// Добавить публичный тег (POST /items/:id/public-tag)
pubTag, err := marketClient.Managing.PublicTag(ctx, 123, nil)

// Удалить публичный тег (DELETE /items/:id/public-tag)
pubUntag, err := marketClient.Managing.PublicUntag(ctx, 123, nil)

// Добавить в избранное (POST /items/:id/favorite)
fav, err := marketClient.Managing.Favorite(ctx, 123)

// Убрать из избранного (DELETE /items/:id/favorite)
unfav, err := marketClient.Managing.Unfavorite(ctx, 123)

// Закрепить аккаунт (POST /items/:id/stick)
stick, err := marketClient.Managing.Stick(ctx, 123)

// Открепить аккаунт (DELETE /items/:id/stick)
unstick, err := marketClient.Managing.Unstick(ctx, 123)

// Передать аккаунт (POST /items/:id/transfer)
transfer, err := marketClient.Managing.Transfer(ctx, 123, nil)
```

### Профиль / Profile

```go
// Получить жалобы профиля (GET /profile/claims)
claims, err := marketClient.Profile.Claims(ctx, nil)

// Получить профиль (GET /profile)
profile, err := marketClient.Profile.Get(ctx, nil)

// Редактировать профиль (PUT /profile)
editProfile, err := marketClient.Profile.Edit(ctx, nil)
```

### Корзина / Cart

```go
// Получить корзину (GET /cart)
cart, err := marketClient.Cart.Get(ctx, nil)

// Добавить в корзину (POST /cart)
addToCart, err := marketClient.Cart.Add(ctx, &market.CartAddBody{ItemID: 123})

// Удалить из корзины (DELETE /cart)
delFromCart, err := marketClient.Cart.Delete(ctx, nil)
```

### Покупка / Purchasing

```go
// Быстрая покупка (POST /items/:id/fast-buy)
fastBuy, err := marketClient.Purchasing.FastBuy(ctx, 123, nil)

// Проверить аккаунт перед покупкой (GET /items/:id/check)
check, err := marketClient.Purchasing.Check(ctx, 123)

// Подтвердить покупку (POST /items/:id/confirm)
confirm, err := marketClient.Purchasing.Confirm(ctx, 123, nil)

// Запросить скидку (POST /items/:id/discount-request)
discountReq, err := marketClient.Purchasing.DiscountRequest(ctx, 123, nil)

// Отменить запрос скидки (DELETE /items/:id/discount-request)
discountCancel, err := marketClient.Purchasing.DiscountCancel(ctx, 123)
```

### Кастомные скидки / Custom Discounts

```go
// Получить кастомные скидки (GET /custom-discounts)
discounts, err := marketClient.CustomDiscounts.Get(ctx)

// Создать скидку (POST /custom-discounts)
createDiscount, err := marketClient.CustomDiscounts.Create(ctx, nil)

// Редактировать скидку (PUT /custom-discounts)
editDiscount, err := marketClient.CustomDiscounts.Edit(ctx, nil)

// Удалить скидку (DELETE /custom-discounts)
delDiscount, err := marketClient.CustomDiscounts.Delete(ctx, nil)
```

### Публикация / Publishing

```go
// Быстрая продажа (POST /items/fast-sell)
fastSell, err := marketClient.Publishing.FastSell(ctx, nil)

// Добавить аккаунт (POST /items/add)
addItem, err := marketClient.Publishing.Add(ctx, nil)

// Проверить аккаунт (POST /items/:id/check)
checkPub, err := marketClient.Publishing.Check(ctx, 123, nil)

// Внешний аккаунт (POST /items/:id/external)
external, err := marketClient.Publishing.External(ctx, 123, nil)
```

### Платежи / Payments

```go
// Получить инвойс (GET /payments/invoice)
invoice, err := marketClient.Payments.InvoiceGet(ctx, nil)

// Создать инвойс (POST /payments/invoice)
createInvoice, err := marketClient.Payments.InvoiceCreate(ctx, nil)

// Список инвойсов (GET /payments/invoice/list)
invoiceList, err := marketClient.Payments.InvoiceList(ctx, nil)

// Получить курсы валют (GET /payments/currency)
currency, err := marketClient.Payments.Currency(ctx)

// Получить список балансов (GET /payments/balance)
balances, err := marketClient.Payments.BalanceList(ctx)

// Обмен валюты (POST /payments/balance/exchange)
exchange, err := marketClient.Payments.BalanceExchange(ctx, nil)

// Перевод средств (POST /payments/transfer)
payTransfer, err := marketClient.Payments.Transfer(ctx, nil)

// Получить комиссию (GET /payments/fee)
fee, err := marketClient.Payments.Fee(ctx, nil)

// Отменить платёж (POST /payments/cancel)
cancelPay, err := marketClient.Payments.Cancel(ctx, nil)

// История платежей (GET /payments/history)
history, err := marketClient.Payments.History(ctx, nil)

// Получить сервисы выплат (GET /payments/payout-services)
payoutServices, err := marketClient.Payments.PayoutServices(ctx)

// Создать выплату (POST /payments/payout)
payout, err := marketClient.Payments.Payout(ctx, nil)
```

### Автоплатежи / Auto Payments

```go
// Список автоплатежей (GET /auto-payments)
autoPayList, err := marketClient.AutoPayments.List(ctx)

// Создать автоплатёж (POST /auto-payments)
autoPayCreate, err := marketClient.AutoPayments.Create(ctx, nil)

// Удалить автоплатёж (DELETE /auto-payments)
autoPayDelete, err := marketClient.AutoPayments.Delete(ctx, nil)
```

### Прокси / Proxy (Market)

```go
// Получить прокси (GET /proxy)
proxyGet, err := marketClient.Proxy.Get(ctx)

// Добавить прокси (POST /proxy)
proxyAdd, err := marketClient.Proxy.Add(ctx, &market.ProxyAddBody{ProxyString: "http://127.0.0.1:8080"})

// Удалить прокси (DELETE /proxy)
proxyDel, err := marketClient.Proxy.Delete(ctx, nil)
```

### IMAP

```go
// Создать IMAP-подключение (POST /imap)
imapCreate, err := marketClient.Imap.Create(ctx, nil)

// Удалить IMAP-подключение (DELETE /imap)
imapDelete, err := marketClient.Imap.Delete(ctx, nil)
```

### Batch (Market)

```go
// Выполнить batch-запросы (POST /batch)
marketBatch, err := marketClient.Batch.Batch(ctx, []market.BatchBatchItem{{Method: "GET", URI: "/category"}})
```

---

## Генерация кода / Code Generation

Клиенты и типы автоматически генерируются из OpenAPI 3.1.0 спецификаций в `schemas/`:

```bash
make generate
# или
go run ./cmd/codegen
```

| Input | Output |
|-------|--------|
| `schemas/forum.json` | `forum/client.go`, `forum/models.go` |
| `schemas/market.json` | `market/client.go`, `market/models.go` |

Исходный код генератора: `cmd/codegen/`, `internal/codegen/`.

---

## Сборка и тесты / Build & Test

```bash
make generate    # Сгенерировать клиенты из OpenAPI-спецификаций
go build ./...   # Собрать проект
go vet ./...     # Линтинг
go test ./...    # Запуск тестов
```

---

## Структура проекта / Project Structure

```
schemas/                    OpenAPI 3.1.0 спецификации
cmd/codegen/                Точка входа генератора кода
internal/codegen/           Реализация генератора кода
client.go                   HTTP-клиент, retry, rate limiter, прокси
errors.go                   Типизированная иерархия ошибок
forum/                      Сгенерированный Forum-клиент и типы
market/                     Сгенерированный Market-клиент и типы
Makefile
go.mod                      Определение модуля (ноль зависимостей)
```

---

## Лицензия / License

[MIT](LICENSE)
