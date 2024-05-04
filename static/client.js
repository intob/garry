const wform = document.getElementById("write")
const lform = document.getElementById("list")
const gatewayInput = document.querySelector("input[name=\"gateway\"]")
gatewayInput.value = window.location.origin

wform.onsubmit = async e => {
    e.preventDefault()
    const button = wform.querySelector("button[type=\"submit\"]")
    button.setAttribute("disabled","")
    const gateway = gatewayInput.value
    const output = wform.querySelector("input[name=\"work\"]")
    const val = wform.querySelector("input[name=\"val\"]").value
    const encoder = new TextEncoder()
    const valBytes = encoder.encode(val)
    const difficulty = wform.querySelector("input[name=\"difficulty\"]").value
    const time = Date.now()
    const {workhash,salt} = await work(valBytes, time, difficulty, button)
    output.value = bytesToHex(workhash)
    const body = { val, time, salt: bytesToHex(salt), work: bytesToHex(workhash) }
    button.textContent = "Sending..."
    const resp = await fetch(gateway, { method: "POST", body: JSON.stringify(body)})
    button.textContent = "Send"
    button.removeAttribute("disabled")
    if (resp.status !== 200) {
        output.value = `${resp.statusText}: ${await resp.text()}`
        return
    }
    const link = wform.querySelector("a#work")
    link.href = `/${bytesToHex(workhash)}`
    link.textContent = bytesToHex(workhash)
}

lform.onsubmit = async e => {
    e.preventDefault()
    const button = lform.querySelector("button[type=\"submit\"]")
    button.setAttribute("disabled","")
    const gateway = gatewayInput.value
    const prefix = lform.querySelector("input[name=\"prefix\"]").value
    let dats = await fetch(`${gateway}/list/${prefix}`)
    const results = lform.querySelector("#results")
    while (results.firstChild) {
        results.removeChild(results.firstChild)
    }
    button.removeAttribute("disabled")
    if (!dats.ok) {
        r = document.createElement("div")
        r.textContent = `Error: ${dats.statusText}: ${await dats.text()}`
        results.appendChild(r)
        return
    }
    for (let dat of (await dats.json()).sort((a,b) => b.added - a.added)) {
        r = document.createElement("div")
        r.textContent = dat.val
        results.appendChild(r)
    }
}

async function work(valBytes, time, difficulty, button) {
    const timeBytes = new Uint8Array(8)
    const dv = new DataView(timeBytes.buffer)
    dv.setBigUint64(0, BigInt(time), false)
    const load = new Uint8Array(valBytes.length + timeBytes.length)
    load.set(valBytes)
    load.set(timeBytes, valBytes.length)
    const loadhash = new Uint8Array(await crypto.subtle.digest("SHA-256", load))
    const salt = new Uint8Array(32)
    const saltBytes = new Uint8Array(32)
    const input = new Uint8Array(32 + 32)
    input.set(loadhash)
    let i = 0
    while (true) {
        if (++i % 1000 == 0) {
            button.textContent = i
        }
        crypto.getRandomValues(salt)
        saltBytes.set(salt)
        input.set(salt, loadhash.length)
        const hashBuffer = await crypto.subtle.digest("SHA-256", input)
        const workhash = new Uint8Array(hashBuffer)
        if (done(workhash, difficulty)) {
            return { workhash, salt } 
        }
    }
}


function done(work, difficulty) {
    for (let i = 0; i < difficulty; i++) {
        if (work[i] !== 0) {
            return false
        }
    }
    return true
}

function bytesToHex(bytes) {
    const hex = new Array(bytes.length * 2);
    for (let i = 0; i < bytes.length; i++) {
        const value = bytes[i];
        hex[i * 2] = value >>> 4;
        hex[i * 2 + 1] = value & 0x0F;
    }
    return hex.map(x => x.toString(16)).join("");
}

