package ui

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/realtime"
)

func TestEventsResumeProtocolPreservesQueueAndScope(t *testing.T) {
	for _, name := range []string{"same-generation", "new-generation", "first-502", "fresh-page", "header-wins"} {
		t.Run(name, func(t *testing.T) {
			hub := realtime.NewHub()
			s := &Server{hub: hub}
			hub.Publish("operator", realtime.Event{Name: "old", Shell: "shell-A"})
			hub.Publish("operator", realtime.Event{Name: "missed", Shell: "shell-A"})
			hub.Publish("someone-else", realtime.Event{Name: "foreign-user"})
			hub.Publish("operator", realtime.Event{Name: "foreign-shell", Shell: "shell-B"})
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				s.eventsStream(w, r.WithContext(auth.ContextWithUser(r.Context(), &auth.User{ID: "test-user", Login: "operator"})))
			}))
			defer srv.Close()
			q := url.Values{"shell": {"shell-A"}, "resume_protocol": {"1"}}
			expected := []string{}
			switch name {
			case "same-generation", "header-wins":
				q.Set("resume", "1")
				q.Set("generation", hub.Generation())
				q.Set("last_event_id", "1")
				if name == "same-generation" {
					expected = []string{"missed"}
				}
			case "new-generation":
				q.Set("resume", "1")
				q.Set("generation", "previous-server")
				q.Set("last_event_id", "999")
				expected = []string{"old", "missed"}
			case "first-502":
				q.Set("resume", "1")
				expected = []string{"old", "missed"}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"?"+q.Encode(), nil)
			if name == "header-wins" {
				req.Header.Set("Last-Event-ID", "2")
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != 200 {
				t.Fatal(resp.StatusCode)
			}
			br := bufio.NewReader(resp.Body)
			var control struct{ System, Data string }
			if err := json.Unmarshal([]byte(readDataFrame(t, br)), &control); err != nil {
				t.Fatal(err)
			}
			if control.System != "stream-generation" || control.Data != hub.Generation() {
				t.Fatal("missing stream generation")
			}
			hub.Publish("operator", realtime.Event{Name: "live", Shell: "shell-A"})
			expected = append(expected, "live")
			for _, want := range expected {
				var event struct{ Name string }
				if err := json.Unmarshal([]byte(readDataFrame(t, br)), &event); err != nil {
					t.Fatal(err)
				}
				if event.Name != want {
					t.Fatalf("got %q want %q", event.Name, want)
				}
			}
		})
	}
}

func TestEventsClientReconnectLifecycle(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	raw, err := os.ReadFile("static/ui.js")
	if err != nil {
		t.Fatal(err)
	}
	source := string(raw)
	a := strings.Index(source, "  // BEGIN onebase-sse-reconnect")
	b := strings.Index(source, "  // END onebase-sse-reconnect")
	if a < 0 || b < a {
		t.Fatal("SSE controller markers missing")
	}
	snippet, _ := json.Marshal(source[a:b])
	code := fmt.Sprintf(`
const assert=require('node:assert/strict');const vm=require('node:vm');
const snippet=%s;
function harness(embedded=false){
 const sources=[],timers=new Map(),listeners={},events=[],forwarded=[],delays=[];let timerID=0;
 const win={__obEmbedded:embedded,addEventListener:(n,f)=>listeners[n]=f};
 class Source{constructor(url){this.url=url;this.readyState=0;sources.push(this)}close(){this.closed=true;this.readyState=2}open(){this.readyState=1;this.onopen()}message(name,id,data){this.onmessage({lastEventId:id||'',data:JSON.stringify({name,data})})}generation(value){this.onmessage({data:JSON.stringify({system:'stream-generation',data:value}),lastEventId:''})}error(){this.readyState=2;this.onerror()}}
 const math=Object.create(Math);math.random=()=>0;
 const ctx={window:win,document:{readyState:'complete',body:{getAttribute:()=>null},addEventListener:(n,f)=>listeners[n]=f},EventSource:Source,obShellId:()=>embedded?'':'shell-A',emitOnebaseEvent:(name,data)=>events.push({name,data}),forwardOnebaseEvent:m=>forwarded.push(m),deliverServerMessage:msg=>{if(msg.name){events.push({name:'onebase:'+msg.name,data:msg.data});forwarded.push(msg)}},obHandleDevSystem:()=>false,location:{reload:()=>assert.fail('unexpected reload')},toast:()=>{},Math:math,setTimeout:(f,d)=>{const id=++timerID;timers.set(id,{f,d});delays.push(d);return id},clearTimeout:id=>timers.delete(id)};
 vm.runInNewContext(snippet,ctx);
 return {win,sources,timers,listeners,events,forwarded,delays,next(){assert.equal(timers.size,1);const [id,t]=timers.entries().next().value;timers.delete(id);t.f();return sources.at(-1)}};
}
{
 const h=harness();const first=h.sources[0];first.open();first.generation('before');first.message('seen','99',{});
 first.error();assert.equal(first.closed,true);assert.equal(h.win.__obEvents,null);first.onerror();assert.equal(h.timers.size,1);
 const failed=h.next();let url=new URL(failed.url,'http://local');assert.equal(url.searchParams.get('last_event_id'),'99');assert.equal(url.searchParams.get('generation'),'before');assert.equal(url.searchParams.get('resume'),'1');assert.equal(url.searchParams.get('shell'),'shell-A');failed.error();
 const restored=h.next();restored.open();restored.generation('after');restored.message('ui.открытьФорму','1',{entity:'SyntheticCall'});
 assert.equal(h.events.filter(x=>x.name==='onebase:ui.открытьФорму').length,1);assert.equal(h.forwarded.filter(x=>x.name==='ui.открытьФорму').length,1);
 assert.equal(h.events.filter(x=>x.name==='onebase:__oblive_refresh_all__').length,1);assert.equal(h.timers.size,0);
 restored.error();const resumed=h.next();url=new URL(resumed.url,'http://local');assert.equal(url.searchParams.get('last_event_id'),'1');assert.equal(url.searchParams.get('generation'),'after');
 resumed.error();h.listeners.pagehide();assert.equal(h.timers.size,0);assert.equal(h.win.__obEvents,null);const count=h.sources.length;resumed.onerror();assert.equal(h.sources.length,count);assert.equal(h.timers.size,0);
 h.listeners.pageshow({persisted:true});assert.equal(h.sources.length,count+1);const current=h.sources.at(-1);const refresh=h.events.length;restored.onopen();assert.equal(h.events.length,refresh);current.open();assert.equal(h.timers.size,0);
}
{
 const h=harness();for(let n=0;n<7;n++){h.sources.at(-1).error();h.next()}assert.deepEqual(h.delays,[1000,2000,4000,5000,5000,5000,5000]);assert.equal(h.sources.filter(x=>!x.closed).length,1);
}
{const h=harness(true);assert.equal(h.sources.length,0);assert.equal(h.timers.size,0)}
console.log('SSE reconnect lifecycle PASS');
`, snippet)
	if output, err := exec.Command(node, "-e", code).CombinedOutput(); err != nil {
		t.Fatalf("node: %v\n%s", err, output)
	}
}
