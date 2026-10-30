package sitecontent

// defaultPhotoURL is also the public fallback when an uploaded photo is gone.
const defaultPhotoURL = "/images/sayantan.jpg"

// defaultVideoID is the owner's featured video.
const defaultVideoID = "gXT9izr0ljQ"

// Default is the site as it was before the copy became editable, so a
// missing row changes nothing visible. {years} stands for
// identity.experience_years wherever the text means years of experience.
func Default() Content {
	featured := Video{YouTubeID: defaultVideoID, Title: "Featured video", Description: "", PublishedOn: ""}
	return Content{
		Identity: Identity{
			Name:            "Sayantan Chakraborty",
			Initials:        "SC",
			ShortRole:       "Oracle DBA & Database Reliability Engineer",
			Location:        "Bangalore, India",
			City:            "Bangalore",
			CountryCode:     "IN",
			ExperienceYears: 11,
			PhotoURL:        defaultPhotoURL,
			Summary: "Oracle Database Administrator and Database Reliability Engineer with {years}+ years of experience delivering high-availability " +
				"solutions for enterprise, government and healthcare sectors, with deep experience in migration, patching and disaster " +
				"recovery architecture.",
			LinkedInURL:   "https://www.linkedin.com/in/sayantan-chakraborty-y2022",
			GitHubURL:     "https://github.com/ust-sayantan",
			YouTubeURL:    "https://www.youtube.com/@csayantan",
			YouTubeHandle: "@csayantan",
			OtherLinks:    []Link{},
			Education: []Education{
				{Degree: "Master of Computer Applications (MCA)", School: "MAKAUT", Years: "2006 – 2009"},
				{Degree: "B.Sc", School: "University of Calcutta", Years: "2001 – 2006"},
			},
			Languages: []string{"English", "Bengali", "Hindi"},
		},
		Navigation: Navigation{
			BrandSubtitle: "Oracle DBA · DRE",
			ResumeLabel:   "Request resume",
			MegaWork:      NavCard{Title: "Selected work", Text: "HA, DR, patching and automation outcomes."},
			MegaVideos:    NavCard{Title: "Videos", Text: "Walkthroughs on YouTube @csayantan."},
			MegaCareer:    NavCard{Title: "Career timeline", Text: "Fifteen years from EBS to DRE."},
			MegaBlog:      NavCard{Title: "Blog & notes", Text: "Oracle DBA articles and lessons learned."},
		},
		Hero: Hero{
			Eyebrow:        "Oracle DBA · Database Reliability Engineer · Bangalore",
			Title:          "Keeping critical databases",
			TitleHighlight: "available, fast and recoverable.",
			Lead: "I'm Sayantan Chakraborty. For {years}+ years I've run Oracle RAC, Data Guard, GoldenGate and EBS estates for " +
				"enterprise, government and healthcare teams, with 24x7 operations and tested disaster recovery.",
			PrimaryCTALabel:     "Explore selected work",
			ResumeCTALabel:      "Verify email to get resume",
			AvailabilityVisible: true,
			AvailabilityText:    "Open to DBA / DRE roles and consulting engagements",
			Metrics: []Metric{
				{Value: "{years}+ years", Label: "Oracle, RAC, Data Guard and EBS in production"},
				{Value: "125 TB", Label: "456 databases across 180 servers"},
				{Value: "99.9%+", Label: "availability in 24x7 operations"},
			},
		},
		About: About{
			Visible:     true,
			Label:       "About",
			StampKicker: "Principles",
			StampLines:  []string{"Measure.", "Protect.", "Recover."},
			StampText:   "Observable systems, rehearsed recovery and calm incident response.",
			Kicker:      "A little context",
			Heading:     "Deep Oracle expertise, with the whole platform in mind.",
			ShowSummary: true,
			Paragraphs: []string{"Most recently I led cloud infrastructure services for Equifax production at UST, keeping GoldenGate " +
				"replication within sub-second SLAs. Before that I ran Oracle and PostgreSQL platforms at IIT Delhi and Ndedge " +
				"Informatics, and spent years implementing Oracle EBS for public-sector and manufacturing clients."},
			Skills: []string{"Oracle 9i–19c", "Oracle RAC", "Data Guard", "RMAN", "ASM", "GoldenGate", "OEM 13c", "AWR / ASH",
				"Data Pump", "Oracle EBS 11i / R12", "PostgreSQL & Patroni", "Ansible", "Terraform", "Docker", "AWS", "Google Cloud"},
		},
		Experience: Experience{
			Visible:  true,
			Label:    "Experience",
			Heading:  "Built through real production challenges.",
			Intro:    "From Oracle EBS rollouts in 2010 to leading 24x7 database reliability for a global credit bureau.",
			CTALabel: "Discuss my experience",
			Roles: []Role{
				{Period: "Apr 2025 – Jan 2026", Title: "Lead II, Cloud Infrastructure Services", Org: "UST (US Technology International), Bangalore",
					Points: []string{"Directed Equifax production infrastructure across databases, servers and VMs.",
						"Resolved critical incidents across Oracle 11g/12c/19c, GoldenGate XAG and OEM 13c with sub-second replication SLAs.",
						"Mentored junior DBAs and coordinated vulnerability management with zero unplanned downtime."}},
				{Period: "Apr 2024 – Apr 2025", Title: "Senior Subject-Matter Expert", Org: "Ndedge Informatics, Kolkata",
					Points: []string{"Migrated production servers from Oracle Linux 7.9 to 8.2 hosting Oracle 19c.",
						"Quarterly CPU/PSU patching across HA environments with zero unplanned downtime."}},
				{Period: "Aug 2023 – Mar 2024", Title: "Senior Project Scientist", Org: "Indian Institute of Technology Delhi",
					Points: []string{"Containerised Linux infrastructure for 10+ research projects with 99.9% uptime.",
						"PostgreSQL 15/16 with etcd and Patroni; automated database builds in under 30 minutes."}},
				{Period: "Oct 2018 – Mar 2023", Title: "Principal Consultant, Infrastructure Systems", Org: "Ndedge Informatics, Kolkata",
					Points: []string{"Owned 15+ critical healthcare and manufacturing systems; led a team of 4 DBAs.",
						"Migrations with 35% efficiency gains; Terraform IaC on GCP and AWS."}},
			},
			EarlierLabel: "Show earlier roles (2010 – 2023)",
			EarlierRoles: []EarlierRole{
				{Period: "Apr – Aug 2023", Title: "Project Consultant", Org: "NIEPA, Delhi"},
				{Period: "May – Jul 2017", Title: "Oracle EBS Consultant", Org: "Wizertech Informatics, Kolkata"},
				{Period: "Sep 2016 – Feb 2017", Title: "Oracle Database Administrator", Org: "Magna Infotech, Kolkata"},
				{Period: "Jun – Aug 2016", Title: "Software Engineer", Org: "Skilliantech India, Kolkata"},
				{Period: "Jun – Nov 2015", Title: "DBA", Org: "Collebra Technologies, Kolkata"},
				{Period: "Jul 2013 – Jul 2014", Title: "Assistant Officer", Org: "BIL Infratech, Kolkata"},
				{Period: "Dec 2012 – Jul 2013", Title: "EBS Consultant", Org: "CSA Consulting, Kolkata"},
				{Period: "2010 – 2012", Title: "Early roles in Oracle EBS, Linux and DBA work", Org: "Kolkata"},
			},
		},
		Work: Work{
			Visible: true,
			Label:   "Selected work",
			Heading: "Outcomes that hold up in production.",
			Intro:   "Client details stay confidential; the scale, the approach and the measured results are real.",
			Items: []WorkItem{
				{Tag: "Scale", Title: "Large-estate operations", Text: "Directed production infrastructure managing over 125 TB of data across 456 databases and 180 servers."},
				{Tag: "Patching", Title: "250+ Oracle patches", Text: "Executed CPU/PSU and EBS patching across clustered environments spanning Oracle 10g to 19c."},
				{Tag: "Recovery", Title: "Downtime cut by 35%", Text: "RMAN backup/recovery improvements and operational optimisation reduced downtime by 35%."},
				{Tag: "Disaster recovery", Title: "DR objectives met", Text: "Enforced RTO under 4 hours and RPO under 1 hour across 20+ production systems."},
				{Tag: "Incident response", Title: "Critical incidents resolved", Text: "Resolved 15+ critical production incidents involving HA configurations and database performance."},
				{Tag: "Automation", Title: "Automated database builds", Text: "Standardised framework provisioning production-ready databases on VMs in under 30 minutes."},
			},
		},
		Organizations: Organizations{
			Visible: true,
			Label:   "Career ecosystem",
			Heading: "Organisations & engagements",
			Intro:   "A selection of employers and client engagements from my career. Names only; no endorsement implied.",
			Items: []Organization{
				{Name: "UST", Note: "Employer"},
				{Name: "Equifax", Note: "Client engagement"},
				{Name: "IIT Delhi", Note: "Employer"},
				{Name: "Ndedge Informatics", Note: "Employer"},
				{Name: "NIEPA", Note: "Project engagement"},
			},
		},
		Expertise: Expertise{
			Visible: true,
			Label:   "Expertise",
			Heading: "What I bring to the team.",
			Intro:   "Grouped by the problems they solve rather than by tool lists.",
			Items: []ExpertiseItem{
				{Title: "High availability & DR", Text: "RAC, Data Guard and GoldenGate designs with tested DR drills and clear RTO/RPO targets.",
					Chips: []string{"RAC", "Data Guard", "GoldenGate", "DR drills"}},
				{Title: "Performance & operations", Text: "AWR/ASH analysis, SQL tuning, capacity planning and calm 24x7 incident response.",
					Chips: []string{"AWR", "ASH", "SQL tuning", "OEM 13c"}},
				{Title: "Automation & cloud", Text: "Infrastructure as code and repeatable builds across on-premises, AWS and Google Cloud.",
					Chips: []string{"Ansible", "Terraform", "Docker", "AWS / GCP"}},
			},
		},
		Videos: Videos{
			Visible:              true,
			Label:                "Videos",
			Heading:              "Watch the walkthroughs.",
			Intro:                "Hands-on videos on Oracle RAC, Data Guard, RMAN recovery and day-to-day DBA work.",
			ChannelCTALabel:      "Subscribe on YouTube",
			Featured:             featured,
			Items:                []Video{featured},
			IncludeArticleVideos: true,
			PageHeading:          "Videos",
			PageIntro:            "Walkthroughs on Oracle RAC, Data Guard, RMAN recovery and day-to-day DBA work, from the YouTube channel @csayantan.",
			PageCTALabel:         "Open the channel on YouTube",
			EmptyText:            "New videos appear here as they are published. Until then, visit the channel above.",
			PlaceholderTitle:     "Video coming soon",
			PlaceholderText:      "Watch the latest Oracle DBA walkthroughs on YouTube.",
		},
		Blog: Blog{
			Visible:     true,
			Label:       "Notes & insights",
			Heading:     "Ideas worth sharing.",
			Intro:       "Articles on Oracle administration, reliability engineering and lessons from production.",
			CTALabel:    "All articles",
			PageHeading: "Notes & insights",
			PageIntro:   "Oracle DBA, high availability and reliability engineering, written from production experience.",
			EmptyText:   "No articles yet. Subscribe below to hear when the first one is published.",
		},
		Newsletter: Newsletter{
			Visible: true,
			Label:   "Newsletter",
			Heading: "New articles and videos, straight to your inbox.",
			Intro:   "Practical Oracle DBA and database reliability notes, at most a few times a month. Confirm by email; unsubscribe in one click.",
		},
		FAQ: FAQ{
			Visible: true,
			Label:   "Frequently asked",
			Heading: "Quick answers",
			Items: []QA{
				{Q: "What does Sayantan Chakraborty specialise in?", A: "Oracle database administration and database reliability engineering: Oracle RAC, " +
					"Data Guard, GoldenGate, RMAN, Oracle EBS, performance tuning and disaster recovery, plus automation with Ansible and Terraform."},
				{Q: "How many years of experience does he have?", A: "More than {years} years running production Oracle databases for enterprise, " +
					"government and healthcare organisations, including estates of 125 TB, 456 databases and 180 servers."},
				{Q: "Where is he based?", A: "Bangalore, India. He works with teams remotely as well."},
				{Q: "How can I get his resume?", A: `Use "Verify email to get resume" on this site. A one-time code is emailed to you, ` +
					"and the PDF downloads once the code is verified."},
				{Q: "Where can I watch his videos?", A: "On YouTube at @csayantan, where he publishes Oracle DBA and database reliability walkthroughs."},
			},
		},
		Contact: Contact{
			Label:     "Contact",
			Heading:   "Let's talk about the work ahead.",
			Intro:     "Have a role, a migration or a reliability problem in mind? Send a note with the context that matters.",
			ShowLinks: true,
		},
		SEO: SEO{
			HomeTitle: "Sayantan Chakraborty · Oracle DBA & Database Reliability Engineer",
			HomeDescription: "Oracle Database Administrator and Database Reliability Engineer in Bangalore with {years}+ years of RAC, " +
				"Data Guard, GoldenGate, RMAN and Oracle EBS experience. Portfolio, articles and videos.",
			VideosTitle:       "Videos: Oracle DBA walkthroughs",
			VideosDescription: "Video walkthroughs on Oracle RAC, Data Guard, RMAN, GoldenGate and database reliability by Sayantan Chakraborty (YouTube @csayantan).",
			BlogTitle:         "Blog: Oracle DBA notes & insights",
			BlogDescription:   "Articles on Oracle RAC, Data Guard, RMAN, GoldenGate, EBS and database reliability engineering by Sayantan Chakraborty.",
			LLMsSummary: "Portfolio of Sayantan Chakraborty, an Oracle Database Administrator and Database Reliability Engineer " +
				"({years}+ years, Bangalore, India) specialising in Oracle RAC, Data Guard, GoldenGate, RMAN, OEM, Oracle EBS, " +
				"PostgreSQL/Patroni, high availability, disaster recovery and automation with Ansible and Terraform.",
		},
	}
}
