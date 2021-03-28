package app

import (
	"bytes"
	"context"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/vdimir/markify/app/engine"

	"github.com/vdimir/markify/app/view"
	"github.com/vdimir/markify/util"

	"github.com/pkg/errors"

	"github.com/go-chi/chi"
)

const fixedPagesPrefixDir = "/static_pages"

// StartServer listen incoming requsets. Blocking function
func (app *App) StartServer(host string, port uint16) {

	serverURL := host
	if serverURL == "" {
		serverURL = "localhost"
	}
	log.Printf("[INFO] starting server at http://%s:%d\n", serverURL, port)
	app.Addr = fmt.Sprintf("%s:%d", host, port)
	app.httpServer = &http.Server{
		Addr:    app.Addr,
		Handler: app.Routes(),
	}

	err := app.httpServer.ListenAndServe()

	if err != nil && err != http.ErrServerClosed {
		log.Printf("[ERROR] server listening error: %s (http://%s:%d)\n", err, serverURL, port)
	}
	log.Printf("[INFO] server stopped")
}

// Shutdown stop server
func (app *App) Shutdown() {
	log.Print("[WARN] shutdown server")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if app.httpServer != nil {
		if err := app.httpServer.Shutdown(ctx); err != nil {
			log.Printf("[WARN] http shutdown error, %s", err)
		}
		log.Print("[DEBUG] shutdown http server completed")
	}
}

func parseUserInput(r *http.Request) *engine.UserDocumentData {
	return &engine.UserDocumentData{
		Data:             []byte(r.FormValue("data")),
		Syntax:  r.FormValue("syntax"),
	}
}

func (app *App) serverError(err error, w http.ResponseWriter) {
	log.Printf("[ERROR] %v", err)
	ctx := &view.StatusContext{
		Title:     "Error",
		HeaderMsg: "500",
		Msg:       "Internal Server error",
	}
	app.viewTemplate(http.StatusInternalServerError, ctx, w)
}

func (app *App) viewTemplate(code int, ctx view.TemplateContext, w http.ResponseWriter) {
	htmlBuf := &bytes.Buffer{}
	err := app.htmlView.RenderPage(htmlBuf, ctx)

	if err != nil {
		log.Printf("[ERROR] %v", errors.Wrapf(err, "cannot render template %s", ctx.Name()))
		app.serverErrorFallback(w)
		return
	}
	if code > 0 {
		w.WriteHeader(code)
	}
	_, err = htmlBuf.WriteTo(w)
	if err != nil {
		log.Printf("[ERROR] %v", err)
		return
	}
}

func (app *App) serverErrorFallback(w http.ResponseWriter) {
	w.WriteHeader(http.StatusInternalServerError)
	rawHTML := "<!DOCTYPE html>" +
		"<html><head><title>Error</title></head><body>" +
		"Internal Server Error" +
		"</body></html>\n"
	w.Write([]byte(rawHTML))
}

func (app *App) addFixedPages(r chi.Router) {
	createDebugHanlder := func(filePath string, raw bool) func(w http.ResponseWriter, r *http.Request) {
		handler := func(w http.ResponseWriter, r *http.Request) {
			f, _ := app.staticFs.Open(filePath)
			data, _ := ioutil.ReadAll(f)

			doc, err := app.engine.CreateDocument(engine.NewUserDocumentData(data))
			if err != nil {
				panic(err)
			}
			app.viewDocument(doc, "", r.URL.Path, w)
		}
		return handler
	}

	err := util.WalkFiles(app.staticFs, fixedPagesPrefixDir, func(data []byte, filePath string) error {
		name := strings.TrimSuffix(filePath, ".md")
		doc, err := app.engine.CreateDocument(engine.NewUserDocumentData(data))
		if err != nil {
			return err
		}
		handler := func(w http.ResponseWriter, r *http.Request) {
			app.viewDocument(doc, "", r.URL.Path, w)
		}

		rawHandler := func(w http.ResponseWriter, r *http.Request) {
			app.viewRawDocument(doc, "Raw", w)
		}

		if app.cfg.Debug {
			handler = createDebugHanlder(path.Join(fixedPagesPrefixDir, filePath), false)
			rawHandler = createDebugHanlder(path.Join(fixedPagesPrefixDir, filePath), true)
		}

		r.Get("/"+name, handler)
		r.Get("/"+name+"/raw", rawHandler)
		return nil
	})

	if err != nil {
		panic(errors.Wrap(err, "cannot add fixed pages"))
	}
}

// addFileServer sets up a http.FileServer handler to serve static files
func (app *App) addFileServer(r chi.Router, path string) {
	if strings.ContainsAny(path, "{}*/") {
		panic("FileServer path not permit URL parameters slashes.")
	}

	webFs := http.FileServer(app.staticFs)
	fileHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/") {
			app.notFound(w, r)
			return
		}
		webFs.ServeHTTP(w, r)
	})

	r.Method("GET", "/"+path+"/{fileName}", fileHandler)
	r.Method("GET", "/favicon.ico", util.AddRoutePrefix("/public", webFs.ServeHTTP))
}
