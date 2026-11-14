# Reader

A read-it-later library and video player, with RSS and Atom feeds: the Reader and Watch apps
(Watch is the same window opened on the videos).

## The library is Fragments Engine's

What was clipped or captured lives in Fragments Engine. The Reader shows FE's own reader
projection (`fe.reader.item.v1`) and changes it only through FE's reader commands. Basalisk
keeps no copy.

The routes pass a fixed set of calls to the owner's FE integration, with the same credential
as capture delivery; no request input chooses the host.

| Route | FE call |
|---|---|
| `GET /api/reader/items` | `GET /v1/reader/items` with `scope`, `renderer`, `tag`, `state`, `provider`, `since`, `until`, `q`, `later`, `starred`, `archived`, `cursor` |
| `GET /api/reader/items/{id}` | `GET /v1/reader/items/{id}` |
| `GET /api/reader/items/{id}/content` | the full text as Markdown (`format=markdown`), which is what FE keeps at rest |
| `POST /api/reader/items/{id}/commands` | `POST /v1/reader/items/{id}/commands` (`fe.reader.command.v1`) |
| `GET /api/reader/media/{variant}` | `GET /v1/media/variants/{variant}/content`, with byte ranges; images, video, audio, PDF and text only |

Views: Unread, Inbox, Later, Starred, Videos, Everything, Archived. Commands used:
`mark_read`, `mark_unread`, `set_reading_progress`, `set_later`, `set_starred`,
`set_archived`, `add_tag`, `remove_tag`, `append_capture_note` (a highlight: select text,
Highlight). Reading progress is saved a little after scrolling stops and the article reopens
where it was left; reaching the end marks it read.

Pictures in an article are shown only from the copies FE holds. A picture FE does not hold is
left out (its caption stays), so reading never fetches from the source site. A YouTube video
plays in YouTube's no-cookie player; a video FE holds plays here and remembers its place.

## Collections

A collection is FE's named, ordered set of library items (a playlist, a reading list, the
research for one piece); see FE's `docs/reader-collections.md`. The sidebar lists them with
their counts and makes new ones. An item's header shows the collections it is in and adds it
to one, or to a new one. A collection's view lists its items in order; dragging a card onto
another moves it there (the whole order is sent, so reordering waits until every page is
loaded). Deleting a collection leaves its items in the library.

Every change carries the collection's revision. When FE answers that it moved on, the list
is read again and the change is tried once more.

Routes: `GET|POST /api/reader/collections`, `PUT|DELETE /api/reader/collections/{id}`,
`GET|POST /api/reader/collections/{id}/items`,
`DELETE /api/reader/collections/{id}/items/{fragment}`,
`PUT /api/reader/collections/{id}/order`, each passed to the same path under
`/v1/reader/collections` (PUT on a collection is FE's PATCH).

## Feeds

RSS 2.0 and Atom feeds are a second provider, kept by Basalisk (`internal/feeds`, `feeds.db`
beside the database). An entry is not in the library until it is saved: following a feed
copies nothing to FE.

- Follow a feed by its address or its site's address (the page's `<link rel="alternate">` is
  followed).
- Entries are kept as plain text (markup removed), at most 300 per feed; starred and saved
  ones are always kept.
- Feeds are fetched when the Reader opens and every 30 minutes while Basalisk runs, with
  conditional requests, from public addresses only (the clip media fetcher's client), with
  no cookies.
- Save sends the entry's link and text to FE as a capture, tagged `feed`. To keep the whole
  page, open it in the Browser and clip it.

Routes: `GET|POST /api/reader/feeds`, `PUT|DELETE /api/reader/feeds/{id}`,
`POST /api/reader/feeds/refresh`, `POST /api/reader/feeds/read`, `GET /api/reader/entries`,
`GET|PUT /api/reader/entries/{id}`.

## Keys

`j`/`k` move, `m` read or unread, `s` star, `l` later, `e` archive.

## Not yet

The change feed (in review at FE); renaming a collection and setting its kind; feed entry
highlights; importing an OPML list.
