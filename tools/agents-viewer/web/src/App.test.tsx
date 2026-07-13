import { fireEvent, render, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { MemoryRouter } from "react-router-dom"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { App, executedContent, isDefaultVisible, SafeMarkdown, VirtualTranscript } from "@/App"
import { resources } from "@/lib/i18n"
import type { EntryListItem, SessionSummary } from "@/generated/api"

const session:SessionSummary={id:"s1",source:"cli",title:"Hello session",preview:"Preview",createdAt:"2026-07-01T00:00:00.000000Z",updatedAt:"2026-07-01T01:00:00.000000Z",archived:false,entryCount:2,diagnosticCount:0,indexState:"ready",completeness:"complete"}
const entry:EntryListItem={id:"e1",sessionId:"s1",sequence:1,timestamp:"2026-07-01T00:10:00Z",kind:"message",presentation:"user",role:"user",title:"User",primaryPreview:"Hello **world**",secondaryPreview:"",primaryBytes:15,secondaryBytes:0,primaryComplete:true,secondaryComplete:true,defaultCollapsed:false,metadata:{},rawRefCount:1}
type EventSourceHarness={instances:Array<{emit:(name:string,data:unknown)=>void}>}
const eventSources=()=>EventSource as unknown as EventSourceHarness
const callsFor=(fragment:string)=>vi.mocked(fetch).mock.calls.filter(([input])=>String(input).includes(fragment)).length

beforeEach(()=>{
  localStorage.clear()
  Object.defineProperty(navigator,"clipboard",{configurable:true,value:{writeText:vi.fn().mockResolvedValue(undefined)}})
  eventSources().instances.length=0
  vi.stubGlobal("fetch",vi.fn(async(input:string|URL|Request)=>{
    const url=String(input)
    let body:unknown
    if(url.includes("/sessions/s1/entries/e1/content")){const secondary=url.includes("field=secondary");body={field:secondary?"secondary":"primary",text:secondary?"":"Hello **world**",byteOffset:0,totalBytes:secondary?0:15,complete:true}}
    else if(url.includes("/sessions/s1/entries/e1"))body={item:entry,derivedMetadata:{},rawRefs:[{id:"r1",line:1,byteOffset:0,byteLength:10,envelopeType:"event_msg"}]}
    else if(url.includes("/sessions/s1/raw/r1"))body={summary:{id:"r1",sessionId:"s1",line:1,byteOffset:0,byteLength:10,envelopeType:"event_msg",parseStatus:"valid",encoding:"utf8",oversize:false},chunk:{field:"primary",text:"{\"safe\":true}",byteOffset:0,totalBytes:13,complete:true}}
    else if(url.includes("/sessions/s1/entries"))body={data:[entry],partial:false}
    else if(url.endsWith("/sessions/s1"))body={summary:session,diagnostics:[]}
    else if(url.includes("/sessions"))body={data:[session],partial:false}
    else if(url.includes("/search"))body={data:[{session,entryId:"e1",kind:"message",snippet:"Hello world",matchRanges:[{start:0,end:5}],field:"primary",rank:1}],partial:false}
    else body={appVersion:"0.1.0",sourceHome:"/source",cacheDir:"/cache",initialIndexDays:7,initialIndexCutoff:"2026-01-01T00:00:00Z",generation:1,phase:"ready",progress:{totalFiles:1,processedFiles:1,totalBytes:1,processedBytes:1,failedFiles:0,excludedFiles:0,excludedBytes:0},ftsReady:true,databaseBytes:1}
    return new Response(JSON.stringify(body),{status:200,headers:{"content-type":"application/json"}})
  }))
})

describe("Agents Viewer UI",()=>{
  it("keeps English and Chinese locale keys identical",()=>{expect(Object.keys(resources.en.translation).sort()).toEqual(Object.keys(resources["zh-CN"].translation).sort())})
  it("renders session, deep link, inspector raw chunk, search, and SSE-safe states",async()=>{
    const user=userEvent.setup();render(<MemoryRouter initialEntries={["/sessions/s1?entry=e1"]}><App/></MemoryRouter>)
    expect(await screen.findByRole("heading",{name:"Hello session"})).toBeInTheDocument()
    expect(await screen.findByText("world")).toBeInTheDocument()
    expect(screen.queryByRole("complementary",{name:"Inspector"})).not.toBeInTheDocument()
    await user.click(screen.getByRole("button",{name:"Filter"}))
    expect(screen.getByRole("option",{name:"Code review"})).toBeInTheDocument()
    await user.click(screen.getByRole("checkbox",{name:/Show technical activity/}))
    await user.click(screen.getByRole("button",{name:"Apply"}))
    await waitFor(()=>expect(vi.mocked(fetch).mock.calls.some(([input])=>String(input).includes("includeTechnical=true"))).toBe(true))
    expect(localStorage.getItem("agents-viewer-show-technical")).toBe("true")
    const inspectorButtons=screen.getAllByRole("button",{name:"Open inspector"});await user.click(inspectorButtons.at(-1)!)
    expect(await screen.findByRole("complementary",{name:"Inspector"})).toBeInTheDocument()
    const rawRecords=await screen.findAllByText("#1 event_msg");await user.click(rawRecords[0]);expect((await screen.findAllByText(/safe/)).length).toBeGreaterThan(0)
    fireEvent.keyDown(window,{key:"k",ctrlKey:true});expect(await screen.findByRole("dialog",{name:"Search"})).toBeInTheDocument();await user.type(screen.getByRole("combobox",{name:"Search"}),"Hello");expect(await screen.findByText("Hello world")).toBeInTheDocument();await user.click(screen.getByRole("checkbox",{name:/Search all activity types/}));await waitFor(()=>expect(vi.mocked(fetch).mock.calls.some(([input])=>String(input).includes("allTypes=true"))).toBe(true));expect(localStorage.getItem("agents-viewer-search-all-types")).toBe("true")
  })
  it("sanitizes raw HTML, scripts, and remote images while rendering GFM",()=>{const {container}=render(<SafeMarkdown text={'# Heading\n\n<script>alert(1)</script>\n\n![secret](https://evil.test/x)\n\n| A | B |\n| - | - |\n| 1 | `code` |\n\n[safe](https://example.com)'}/>);expect(document.querySelector("script")).toBeNull();expect(document.querySelector("img")).toBeNull();expect(screen.getByText(/Attachment/)).toBeInTheDocument();expect(screen.getByRole("link",{name:"safe"})).toHaveAttribute("rel","noreferrer noopener");expect(container.querySelector("table")).toBeInTheDocument();expect(container.querySelector("code")).toHaveTextContent("code")})
  it("does not request session filters until Apply",async()=>{const user=userEvent.setup();render(<MemoryRouter initialEntries={["/sessions/s1"]}><App/></MemoryRouter>);expect(await screen.findByRole("heading",{name:"Hello session"})).toBeInTheDocument();const before=callsFor("/api/v1/sessions?");await user.click(screen.getByRole("button",{name:"Filter"}));await user.type(screen.getByRole("textbox",{name:"Working directory"}),"/work/demo");expect(callsFor("/api/v1/sessions?")).toBe(before);await user.click(screen.getByRole("button",{name:"Cancel"}));await user.click(screen.getByRole("button",{name:"Filter"}));await user.type(screen.getByRole("textbox",{name:"Working directory"}),"/work/demo");await user.click(screen.getByRole("button",{name:"Apply"}));await waitFor(()=>expect(callsFor("/api/v1/sessions?")).toBe(before+1));expect(vi.mocked(fetch).mock.calls.some(([input])=>String(input).includes("cwd=%2Fwork%2Fdemo"))).toBe(true)})
  it("renders copyable chat bubbles and two-column activity without per-item times",async()=>{const user=userEvent.setup();const writeText=vi.spyOn(navigator.clipboard,"writeText");const inspect=vi.fn();const reasoning={...entry,id:"e2",sequence:2,kind:"reasoning" as const,presentation:"technical" as const,role:undefined,title:"Reasoning",primaryPreview:"First useful line",defaultCollapsed:true};const command={...entry,id:"e3",sequence:3,kind:"tool" as const,presentation:"technical" as const,role:undefined,toolKind:"command" as const,title:"exec_command",primaryPreview:'{"cmd":"printf hello\\nprintf world"}',secondaryPreview:"secret output",defaultCollapsed:true};const {container}=render(<VirtualTranscript entries={[entry,reasoning,command]} onInspect={inspect}/>);expect(container.querySelector(".message-user")).toBeInTheDocument();expect(screen.getByText("Reasoning:")).toHaveClass("activity-label");expect(screen.getByText("First useful line")).toHaveClass("activity-body");expect(screen.getByText("Executing:")).toHaveClass("activity-label");const executingButton=screen.getByRole("button",{name:/Executing: printf hello/});expect(executingButton.querySelector(".activity-body")).toHaveTextContent(/printf hello\s+printf world/);expect(screen.queryByText("secret output")).not.toBeInTheDocument();const reasoningButton=screen.getByRole("button",{name:/Reasoning: First useful line/});expect(within(reasoningButton).queryByRole("time")).not.toBeInTheDocument();await user.click(reasoningButton);expect(inspect).toHaveBeenCalledWith("e2");await user.click(screen.getByRole("button",{name:"Copy message"}));expect(writeText).toHaveBeenCalledWith("Hello **world**");expect(executedContent('{"action":{"command":["git","status"]}}')).toBe("git status");expect(isDefaultVisible(command)).toBe(true)})
  it("loads complete message content before copying a truncated bubble",async()=>{const user=userEvent.setup();const writeText=vi.spyOn(navigator.clipboard,"writeText");render(<VirtualTranscript entries={[{...entry,primaryPreview:"Hello…",primaryComplete:false}]} onInspect={()=>{}}/>);await user.click(screen.getByRole("button",{name:"Copy message"}));await waitFor(()=>expect(writeText).toHaveBeenCalledWith("Hello **world**"));expect(vi.mocked(fetch).mock.calls.some(([input])=>String(input).includes("/entries/e1/content?field=primary"))).toBe(true)})
  it("renders true-boundary navigation and keeps a 10,000-entry transcript below 200 DOM rows",async()=>{const user=userEvent.setup();const top=vi.fn();const bottom=vi.fn();const entries=Array.from({length:10000},(_,index)=>({...entry,id:`e${index}`,sequence:index}));const {container}=render(<VirtualTranscript entries={entries} hasOlder hasNewer newCount={3} onInspect={()=>{}} onJumpTop={top} onJumpBottom={bottom}/>);await waitFor(()=>expect(container.querySelectorAll("[data-transcript-entry]").length).toBeGreaterThan(0));expect(container.querySelectorAll("[data-transcript-entry]").length).toBeLessThan(200);await user.click(screen.getByRole("button",{name:"Go to first message"}));await user.click(screen.getByRole("button",{name:"Go to 3 new items"}));expect(top).toHaveBeenCalled();expect(bottom).toHaveBeenCalled()})
  it("uses one event stream and coalesces refreshes by event type",async()=>{
    render(<MemoryRouter initialEntries={["/sessions/s1"]}><App/></MemoryRouter>)
    expect(await screen.findByRole("heading",{name:"Hello session"})).toBeInTheDocument()
    expect(eventSources().instances).toHaveLength(1)
    const stream=eventSources().instances[0]
    const listBefore=callsFor("/api/v1/sessions?")
    const statusBefore=callsFor("/api/v1/status")
    stream.emit("heartbeat",{generation:2})
    stream.emit("indexProgress",{generation:2,phase:"indexing",progress:{totalFiles:10,processedFiles:5,totalBytes:10,processedBytes:5,failedFiles:0,excludedFiles:0,excludedBytes:0}})
    expect(await screen.findByText("Indexing 5 / 10")).toBeInTheDocument()
    expect(callsFor("/api/v1/sessions?")).toBe(listBefore)
    expect(callsFor("/api/v1/status")).toBe(statusBefore)
    stream.emit("sessionUpdated",{generation:3,sessionId:"s1"})
    stream.emit("sessionUpdated",{generation:3,sessionId:"s2"})
    stream.emit("sessionUpdated",{generation:3,sessionId:"s3"})
    await waitFor(()=>expect(callsFor("/api/v1/sessions?")).toBe(listBefore+1))
    const entriesBefore=callsFor("/api/v1/sessions/s1/entries")
    stream.emit("entryUpdated",{generation:3,sessionId:"s1",entryId:"e1"})
    await waitFor(()=>expect(callsFor("/api/v1/sessions/s1/entries")).toBe(entriesBefore+1))
    expect(callsFor("/api/v1/sessions?")).toBe(listBefore+1)
  })
  it("allows only one in-flight request for a pagination cursor",async()=>{
    const fallback=vi.mocked(fetch)
    let resolveCursor:(response:Response)=>void=()=>{}
    const pending=new Promise<Response>(resolve=>{resolveCursor=resolve})
    const controlled=vi.fn((input:string|URL|Request,init?:RequestInit)=>{
      const url=String(input)
      if(url.includes("cursor=cursor-1"))return pending
      if(url.includes("/sessions/s1/entries"))return Promise.resolve(new Response(JSON.stringify({data:[entry],previousCursor:"cursor-1",partial:false}),{status:200,headers:{"content-type":"application/json"}}))
      return fallback(input,init)
    })
    vi.stubGlobal("fetch",controlled)
    render(<MemoryRouter initialEntries={["/sessions/s1"]}><App/></MemoryRouter>)
    expect(await screen.findByRole("heading",{name:"Hello session"})).toBeInTheDocument()
    expect(await screen.findByRole("button",{name:"Go to first message"})).toBeInTheDocument()
    const transcript=document.getElementById("transcript-scroll")!
    Object.defineProperties(transcript,{clientHeight:{configurable:true,value:400},scrollHeight:{configurable:true,value:1200}})
    fireEvent.scroll(transcript,{target:{scrollTop:0}})
    fireEvent.scroll(transcript,{target:{scrollTop:0}})
    await waitFor(()=>expect(controlled.mock.calls.filter(([input])=>String(input).includes("cursor=cursor-1"))).toHaveLength(1))
    await new Promise(resolve=>setTimeout(resolve,150))
    expect(controlled.mock.calls.filter(([input])=>String(input).includes("cursor=cursor-1"))).toHaveLength(1)
    resolveCursor(new Response(JSON.stringify({data:[],partial:false}),{status:200,headers:{"content-type":"application/json"}}))
  })
})
