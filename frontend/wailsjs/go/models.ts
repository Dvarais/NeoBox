export namespace core {
	
	export class SubscriptionUserInfo {
	    upload: number;
	    download: number;
	    total: number;
	    expire: number;
	
	    static createFrom(source: any = {}) {
	        return new SubscriptionUserInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.upload = source["upload"];
	        this.download = source["download"];
	        this.total = source["total"];
	        this.expire = source["expire"];
	    }
	}

}

export namespace platform {
	
	export class ProcessInfo {
	    name: string;
	    path: string;
	    count: number;
	
	    static createFrom(source: any = {}) {
	        return new ProcessInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.path = source["path"];
	        this.count = source["count"];
	    }
	}

}

export namespace service {
	
	export class connectionRow {
	    id: string;
	    host: string;
	    destIP: string;
	    destPort: string;
	    network: string;
	    process: string;
	    rule: string;
	    outbound: string;
	    upload: number;
	    download: number;
	    startMs: number;
	    ruleType: string;
	    ruleValue: string;
	
	    static createFrom(source: any = {}) {
	        return new connectionRow(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.host = source["host"];
	        this.destIP = source["destIP"];
	        this.destPort = source["destPort"];
	        this.network = source["network"];
	        this.process = source["process"];
	        this.rule = source["rule"];
	        this.outbound = source["outbound"];
	        this.upload = source["upload"];
	        this.download = source["download"];
	        this.startMs = source["startMs"];
	        this.ruleType = source["ruleType"];
	        this.ruleValue = source["ruleValue"];
	    }
	}
	export class ConnectionsPayload {
	    running: boolean;
	    connections: connectionRow[];
	    total: number;
	
	    static createFrom(source: any = {}) {
	        return new ConnectionsPayload(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.running = source["running"];
	        this.connections = this.convertValues(source["connections"], connectionRow);
	        this.total = source["total"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class LanIPInfo {
	    name: string;
	    ip: string;
	
	    static createFrom(source: any = {}) {
	        return new LanIPInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.ip = source["ip"];
	    }
	}
	export class ServerSpeedPayload {
	    success: boolean;
	    mbps?: number;
	    speed?: number;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new ServerSpeedPayload(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.success = source["success"];
	        this.mbps = source["mbps"];
	        this.speed = source["speed"];
	        this.error = source["error"];
	    }
	}
	export class SubscriptionFetchResult {
	    links: string[];
	    userInfo?: core.SubscriptionUserInfo;
	
	    static createFrom(source: any = {}) {
	        return new SubscriptionFetchResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.links = source["links"];
	        this.userInfo = this.convertValues(source["userInfo"], core.SubscriptionUserInfo);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

