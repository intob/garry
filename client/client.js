const form = document.querySelector("form")
form.onsubmit = async e => {
    e.preventDefault()
    const gateway = form.querySelector("input[name=\"gateway\"]").value
    const val = form.querySelector("input[name=\"val\"]").value
    const tag = form.querySelector("input[name=\"tag\"]").value
    const difficulty = form.querySelector("input[name=\"difficulty\"]").value
    const button = form.querySelector("button[type=\"submit\"]")
    const msg = await work({val, tag}, difficulty, button)
    form.querySelector("input[name=\"work\"]").value = bytesToHex(msg.work)
    const link = form.querySelector("a#work")
    link.href = `/${bytesToHex(msg.work)}`
    link.textContent = bytesToHex(msg.work)
    await fetch(gateway, {
        method: "POST",
        body: JSON.stringify({
            val, tag,
            nonce: bytesToHex(msg.nonce),
            work: bytesToHex(msg.work)
        })
    })
    button.textContent = "Send"
}

async function work(msg, difficulty, button) {
    const encoder = new TextEncoder()
    const valBytes = encoder.encode(msg.val)
    const tagBytes = encoder.encode(msg.tag)
    const hash = await crypto.subtle.digest("SHA-256", concat(valBytes, tagBytes))
    const load = new Uint8Array(hash)
    msg.nonce = new Uint8Array(32)
    const nonceBytes = new Uint8Array(32)
    const input = new Uint8Array(load.length + 32)
    input.set(load)
    let i = 0
    while (true) {
        if (++i % 1000 == 0) {
            button.textContent = i
        }
        crypto.getRandomValues(msg.nonce)
        nonceBytes.set(msg.nonce)
        input.set(msg.nonce, load.length)
        const hashBuffer = await crypto.subtle.digest("SHA-256", input)
        msg.work = new Uint8Array(hashBuffer)
        if (isDone(msg.work, difficulty)) {
            return msg
        } 
    }
}

function isDone(work, difficulty) {
    for (let i = 0; i < difficulty; i++) {
        if (work[i] !== 0) {
            return false
        }
    }
    return true
}

function concat(buffer1, buffer2) {
    const result = new Uint8Array(buffer1.length + buffer2.length)
    result.set(buffer1)
    result.set(buffer2, buffer1.length)
    return result
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
